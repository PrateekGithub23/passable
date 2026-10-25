package overlay

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/geo"
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/PrateekGithub23/passable/internal/hazard"
	"github.com/PrateekGithub23/passable/internal/route"
)

func ice(id int64, edge int32, status hazard.Status) Hazard {
	return Hazard{ID: id, Edge: edge, Type: hazard.IceSnow, Severity: hazard.Minor, Status: status}
}

func TestWithIsCopyOnWrite(t *testing.T) {
	s0 := Build(0, nil)
	s1, err := s0.With(Event{Seq: 1, Hazard: ice(1, 7, hazard.Active)})
	if err != nil {
		t.Fatal(err)
	}
	// The old snapshot must be untouched: queries that loaded it keep their view.
	if s0.Cost(cost.Wheelchair, 7, 100) != 100 || s0.Len() != 0 {
		t.Fatal("s0 was modified")
	}
	if got := s1.Cost(cost.Wheelchair, 7, 100); got != 300 { // ice: wheelchair x3
		t.Fatalf("s1 cost %v, want 300", got)
	}
	// Resolving removes the effect.
	s2, err := s1.With(Event{Seq: 2, Hazard: ice(1, 7, hazard.Resolved)})
	if err != nil {
		t.Fatal(err)
	}
	if s2.Cost(cost.Wheelchair, 7, 100) != 100 || s2.Len() != 0 || s1.Len() != 1 {
		t.Fatal("resolve did not remove the hazard, or modified s1")
	}
}

func TestEventOrdering(t *testing.T) {
	s := Build(5, nil)
	if _, err := s.With(Event{Seq: 5}); !errors.Is(err, ErrStale) {
		t.Errorf("seq 5 on 5: want ErrStale, got %v", err)
	}
	if _, err := s.With(Event{Seq: 7}); !errors.Is(err, ErrGap) {
		t.Errorf("seq 7 on 5: want ErrGap, got %v", err)
	}
	h := NewHolder(s)
	if err := h.Apply(Event{Seq: 4}); err != nil { // stale events are silently ignored
		t.Errorf("stale apply: %v", err)
	}
	if err := h.Apply(Event{Seq: 6, Hazard: ice(1, 0, hazard.Active)}); err != nil || h.Load().Seq != 6 {
		t.Errorf("in-order apply: err %v, seq %d", err, h.Load().Seq)
	}
	h.Replace(Build(3, nil)) // older than current: ignored
	if h.Load().Seq != 6 {
		t.Errorf("Replace moved backwards to %d", h.Load().Seq)
	}
}

func TestCombinedEffects(t *testing.T) {
	s := Build(2, []Hazard{
		ice(1, 3, hazard.Active),
		{ID: 2, Edge: 3, Type: hazard.Construction, Severity: hazard.Minor, Status: hazard.Confirmed},
	})
	// Wheelchair: construction blocks. Stroller: max(ice x2, construction x2) = x2.
	// Walk_winter: max(ice x4, construction x1.5) = x4.
	if !math.IsInf(s.Cost(cost.Wheelchair, 3, 10), 1) {
		t.Error("wheelchair not blocked")
	}
	if got := s.Cost(cost.Stroller, 3, 10); got != 20 {
		t.Errorf("stroller %v, want 20", got)
	}
	if got := s.Cost(cost.WalkWinter, 3, 10); got != 40 {
		t.Errorf("walk_winter %v, want 40", got)
	}
	if len(s.HazardsOn(3)) != 2 || s.HazardsOn(4) != nil {
		t.Error("HazardsOn wrong")
	}
}

// gridGraph is an n x n grid of ~100 m blocks, so many alternative routes exist.
func gridGraph(n int) *graph.Graph {
	var nodes []graph.Node
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			nodes = append(nodes, graph.Node{OSMID: int64(len(nodes)), Lat: 43.46 + float64(r)*0.0009, Lon: -80.55 + float64(c)*0.00124})
		}
	}
	var edges []graph.Edge
	link := func(a, b int) {
		edges = append(edges, graph.Edge{ID: int64(len(edges) + 1), From: int32(a), To: int32(b),
			LengthM: geo.Haversine(nodes[a].Lat, nodes[a].Lon, nodes[b].Lat, nodes[b].Lon)})
	}
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if c+1 < n {
				link(r*n+c, r*n+c+1)
			}
			if r+1 < n {
				link(r*n+c, (r+1)*n+c)
			}
		}
	}
	return graph.New(nodes, edges)
}

// TestConcurrentRoutingSeesConsistentSnapshots is the stress test for the core design:
// routers search continuously while a writer keeps adding and removing hazards. Every route
// must be internally consistent with the ONE snapshot it loaded: its reported cost equals the
// sum of edge costs under that snapshot, and it never uses an edge that snapshot blocks.
// Run with -race to also check for data races.
func TestConcurrentRoutingSeesConsistentSnapshots(t *testing.T) {
	g := gridGraph(20)
	base := make([]float64, len(g.Edges))
	for i, e := range g.Edges {
		base[i] = e.LengthM
	}
	h := NewHolder(Build(0, nil))

	var stop atomic.Bool
	var routes, versionsSeen atomic.Int64
	var wg sync.WaitGroup

	// Writer: hazards on random edges, alternating block (blocked sidewalk) and ice, and
	// resolving old ones, so costs keep changing under the routers' feet.
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := rand.New(rand.NewSource(1))
		seq := int64(0)
		for !stop.Load() {
			seq++
			hz := Hazard{ID: int64(r.Intn(40)), Edge: int32(r.Intn(len(g.Edges))), Severity: hazard.Major, Status: hazard.Active}
			hz.Type = hazard.BlockedSidewalk
			if r.Intn(2) == 0 {
				hz.Type = hazard.IceSnow
			}
			if r.Intn(3) == 0 {
				hz.Status = hazard.Resolved
			}
			if err := h.Apply(Event{Seq: seq, Hazard: hz}); err != nil {
				t.Errorf("apply: %v", err)
				return
			}
		}
	}()

	// Routers.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(100 + w)))
			lastSeq := int64(-1)
			for !stop.Load() {
				snap := h.Load() // the single load for this query
				if snap.Seq != lastSeq {
					versionsSeen.Add(1)
					lastSeq = snap.Seq
				}
				costFn := func(e int32) float64 { return snap.Cost(cost.Wheelchair, e, base[e]) }
				src, dst := int32(r.Intn(len(g.Nodes))), int32(r.Intn(len(g.Nodes)))
				p, err := route.AStar(g, costFn, src, dst)
				if errors.Is(err, route.ErrNoPath) {
					continue
				}
				sum := 0.0
				for _, ei := range p.Edges {
					c := costFn(ei)
					if math.IsInf(c, 1) {
						t.Errorf("route used edge %d blocked in its own snapshot %d", ei, snap.Seq)
						return
					}
					sum += c
				}
				if math.Abs(sum-p.Cost) > 1e-6*math.Max(1, sum) {
					t.Errorf("snapshot %d: path cost %.3f != sum of edge costs %.3f", snap.Seq, p.Cost, sum)
					return
				}
				routes.Add(1)
			}
		}(w)
	}

	time.Sleep(2 * time.Second)
	stop.Store(true)
	wg.Wait()
	t.Logf("%d routes across %d snapshot changes observed by routers; final seq %d, %d live hazards",
		routes.Load(), versionsSeen.Load(), h.Load().Seq, h.Load().Len())
	if routes.Load() == 0 || h.Load().Seq < 100 {
		t.Fatal("stress test did too little work to be meaningful")
	}
}
