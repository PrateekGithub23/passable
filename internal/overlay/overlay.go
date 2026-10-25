// Package overlay is the live-hazard layer on top of the immutable base graph.
//
// The core engineering problem: route queries must see a consistent set of edge costs while
// hazard reports change those costs concurrently, without locks that stall searches.
//
// Solution: copy-on-write snapshots behind an atomic pointer.
//
//   - A Snapshot is immutable once published. It holds the live hazards and, per affected
//     edge, the combined effect for each profile.
//   - A route query calls Holder.Load() ONCE and uses that snapshot for the whole search.
//     Even if hazards change mid-search, this query keeps a consistent view, and it never
//     takes a lock.
//   - A writer builds a NEW snapshot (copying the small hazard maps, never the graph) and
//     swaps the pointer atomically. Old snapshots stay valid for queries still using them,
//     and the garbage collector frees them when the last query finishes.
//
// Snapshot.Seq is the id of the last hazard event applied (hazard_events.seq in Postgres).
// Because every instance applies the same events in the same order, Seq is a global graph
// version: same Seq => same hazards => same routes. The route cache keys on it.
package overlay

import (
	"errors"
	"maps"
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/hazard"
)

// Hazard is a live report as the overlay sees it: only what routing needs.
type Hazard struct {
	ID       int64
	Edge     int32 // edge index in the graph (not the DB id)
	Type     hazard.Type
	Severity hazard.Severity
	Status   hazard.Status
}

// Event says "after hazard event Seq, report Hazard.ID is in this state". It carries the
// report's full new state (not a diff), so applying it is just an upsert or a delete.
type Event struct {
	Seq    int64
	Hazard Hazard
}

// Sentinel errors from Snapshot.With.
var (
	ErrStale = errors.New("overlay: event already applied")                  // Seq <= current: ignore
	ErrGap   = errors.New("overlay: event sequence gap; full resync needed") // Seq > current+1
)

// EdgeEffect is the combined effect of every live hazard on one edge, per profile
// (indexed by cost.Profile.Index()).
type EdgeEffect struct {
	Block   [3]bool
	Mult    [3]float64
	Hazards []int64 // report ids on this edge, for explanations
}

// Snapshot is an immutable view of all live hazards. Never modify one after it is published;
// derive a new one with With.
type Snapshot struct {
	Seq     int64
	hazards map[int64]Hazard     // report id -> hazard
	effects map[int32]EdgeEffect // edge index -> combined effect (only edges with hazards)
}

// Build creates a snapshot from a full list of live hazards (startup and resync).
func Build(seq int64, live []Hazard) *Snapshot {
	s := &Snapshot{Seq: seq, hazards: make(map[int64]Hazard, len(live)), effects: map[int32]EdgeEffect{}}
	touched := map[int32]bool{}
	for _, h := range live {
		s.hazards[h.ID] = h
		touched[h.Edge] = true
	}
	for e := range touched {
		s.recompute(e)
	}
	return s
}

// With returns a new snapshot with ev applied; s itself is not modified.
// Events must arrive in order: ev.Seq must be exactly s.Seq+1.
func (s *Snapshot) With(ev Event) (*Snapshot, error) {
	switch {
	case ev.Seq <= s.Seq:
		return nil, ErrStale // duplicate or already covered by a resync
	case ev.Seq > s.Seq+1:
		return nil, ErrGap // we missed an event; only a full reload is safe
	}
	// Copy-on-write: clone the two small maps (size = number of live hazards / affected
	// edges, typically dozens), then modify the copies.
	next := &Snapshot{Seq: ev.Seq, hazards: maps.Clone(s.hazards), effects: maps.Clone(s.effects)}
	if next.hazards == nil {
		next.hazards = map[int64]Hazard{}
	}
	if next.effects == nil {
		next.effects = map[int32]EdgeEffect{}
	}

	// A report can move edges only in theory (it can't today), so recompute both its old
	// and new edge to be safe.
	if old, ok := next.hazards[ev.Hazard.ID]; ok {
		delete(next.hazards, ev.Hazard.ID)
		next.recompute(old.Edge)
	}
	if ev.Hazard.Status.Live() {
		next.hazards[ev.Hazard.ID] = ev.Hazard
	}
	next.recompute(ev.Hazard.Edge)
	return next, nil
}

// recompute rebuilds the effect for edge e from every live hazard on it. Only called on a
// snapshot that is still being built (not yet published).
func (s *Snapshot) recompute(e int32) {
	var eff EdgeEffect
	for i := range eff.Mult {
		eff.Mult[i] = 1
	}
	found := false
	for _, h := range s.hazards {
		if h.Edge != e {
			continue
		}
		found = true
		eff.Hazards = append(eff.Hazards, h.ID)
		for _, p := range cost.Profiles {
			ef := hazard.EffectOn(h.Type, h.Severity, p)
			i := p.Index()
			// Several hazards on one edge: any block blocks; otherwise the worst multiplier
			// wins (two ice reports on the same path are not twice as icy).
			eff.Block[i] = eff.Block[i] || ef.Block
			eff.Mult[i] = math.Max(eff.Mult[i], ef.Mult)
		}
	}
	if found {
		slices.Sort(eff.Hazards) // map iteration order is random; keep explanations stable
		s.effects[e] = eff
	} else {
		delete(s.effects, e)
	}
}

// Cost returns edge e's cost for profile p given its base (tag-derived) cost.
// This sits on the router's hot path, so the common case (no hazard on this edge) is one
// map lookup, or none at all when there are no hazards.
func (s *Snapshot) Cost(p cost.Profile, e int32, base float64) float64 {
	if len(s.effects) == 0 {
		return base
	}
	eff, ok := s.effects[e]
	if !ok {
		return base
	}
	i := p.Index()
	if eff.Block[i] {
		return math.Inf(1)
	}
	return base * eff.Mult[i]
}

// HazardsOn returns the live hazards on edge e (for explanations).
func (s *Snapshot) HazardsOn(e int32) []Hazard {
	eff, ok := s.effects[e]
	if !ok {
		return nil
	}
	out := make([]Hazard, 0, len(eff.Hazards))
	for _, id := range eff.Hazards {
		out = append(out, s.hazards[id])
	}
	return out
}

// Len is the number of live hazards.
func (s *Snapshot) Len() int { return len(s.hazards) }

// Holder publishes the current snapshot. Reads are lock-free; writes are serialized.
type Holder struct {
	cur atomic.Pointer[Snapshot]
	mu  sync.Mutex // serializes writers so two updates can't both start from the same snapshot
}

// NewHolder starts with the given snapshot.
func NewHolder(s *Snapshot) *Holder {
	h := &Holder{}
	h.cur.Store(s)
	return h
}

// Load returns the current snapshot. Call it once per query and use the result throughout.
func (h *Holder) Load() *Snapshot { return h.cur.Load() }

// Apply publishes ev. Already-applied events are ignored (returns nil); a gap returns ErrGap
// and the caller must Replace with a fresh snapshot from the database.
func (h *Holder) Apply(ev Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	next, err := h.cur.Load().With(ev)
	if errors.Is(err, ErrStale) {
		return nil
	}
	if err != nil {
		return err
	}
	h.cur.Store(next) // the atomic swap: from here on, new queries see the new costs
	return nil
}

// Replace publishes a fully rebuilt snapshot (after a resync). It never moves backwards: a
// snapshot older than the current one is ignored.
func (h *Holder) Replace(s *Snapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.Seq >= h.cur.Load().Seq {
		h.cur.Store(s)
	}
}
