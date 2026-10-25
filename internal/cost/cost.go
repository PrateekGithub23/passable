// Package cost turns edge tags into per-profile traversal costs.
//
// cost = length_m * multiplier + penalty_m, with multiplier >= 1 and penalty >= 0, so a
// cost never undercuts the edge length and the router's haversine heuristic stays
// admissible. Blocked edges cost +Inf. Values are documented in DECISIONS.md.
//
// Example (wheelchair, 100 m untagged footway crossing with no kerb data):
// multiplier 1.3 (surface unknown), penalty 30 m (no kerb data) -> cost 100*1.3 + 30 = 160.
// The router then prefers any alternative that costs less than 160 "effective metres".
package cost

import (
	"fmt"
	"math"
	"slices"

	"github.com/PrateekGithub23/passable/internal/graph"
)

// Profile is a routing profile.
type Profile string

const (
	Wheelchair Profile = "wheelchair"
	Stroller   Profile = "stroller"
	WalkWinter Profile = "walk_winter"
)

// Profiles lists all profiles in display order.
var Profiles = []Profile{Wheelchair, Stroller, WalkWinter}

// Index is the profile's position in Profiles (0, 1, 2). Per-profile data elsewhere (e.g. the
// hazard overlay) is stored in small fixed arrays indexed by it, which is faster than a map.
func (p Profile) Index() int {
	switch p {
	case Wheelchair:
		return 0
	case Stroller:
		return 1
	case WalkWinter:
		return 2
	}
	panic("unknown profile " + string(p))
}

// ParseProfile validates a profile name.
func ParseProfile(s string) (Profile, error) {
	for _, p := range Profiles {
		if string(p) == s {
			return p, nil
		}
	}
	return "", fmt.Errorf("unknown profile %q (want wheelchair, stroller or walk_winter)", s)
}

// Confidence says how much of an edge's accessibility data came from explicit tags.
type Confidence string

const (
	Verified Confidence = "verified" // the relevant tags are present
	Inferred Confidence = "inferred" // a missing tag was filled in from context (e.g. sidewalks are paved)
	Unknown  Confidence = "unknown"  // a relevant tag is missing and could not be inferred
)

// Result is the evaluation of one edge under one profile.
type Result struct {
	Multiplier float64
	PenaltyM   float64
	Blocked    bool
	Reasons    []string // factors that changed the cost, for explanations
	Confidence Confidence
}

// Cost returns the traversal cost for an edge of the given length.
func (r Result) Cost(lengthM float64) float64 {
	if r.Blocked {
		return math.Inf(1) // the router skips +Inf edges entirely
	}
	return lengthM*r.Multiplier + r.PenaltyM
}

// params is one profile's rule set. Fields named *Block turn a factor into a hard block;
// *Mult fields multiply the length; *Pen fields add fixed metres. A zero value means "this
// factor does not matter for this profile" (a multiplier of 0 is ignored, see mult below).
type params struct {
	stepsBlock          bool
	stepsMult, stepsPen float64
	fairMult            float64
	badBlock            bool
	badMult             float64
	unknownSurfaceMult  float64
	smoothIntermediate  float64
	smoothBadBlock      bool
	smoothBadMult       float64
	wheelchairNoBlock   bool
	raisedBlock         bool
	raisedPen           float64
	kerbUnspecifiedPen  float64
	crossingNoKerbPen   float64
}

// The numbers behind the table in DECISIONS.md #8. Keeping them as data (not if/else per
// profile) means every profile runs exactly the same Evaluate logic.
var profileParams = map[Profile]params{
	Wheelchair: {
		stepsBlock: true, fairMult: 1.5, badBlock: true, unknownSurfaceMult: 1.3,
		smoothIntermediate: 1.2, smoothBadBlock: true, wheelchairNoBlock: true,
		raisedBlock: true, kerbUnspecifiedPen: 50, crossingNoKerbPen: 30,
	},
	Stroller: {
		stepsBlock: true, fairMult: 1.2, badMult: 2.0, unknownSurfaceMult: 1.15,
		smoothIntermediate: 1.1, smoothBadMult: 1.5,
		raisedPen: 30, kerbUnspecifiedPen: 10, crossingNoKerbPen: 10,
	},
	// No hard blocks: winter walkers can go anywhere, they just prefer cleared, step-free paths.
	WalkWinter: {
		stepsMult: 2.0, stepsPen: 15, fairMult: 1.2, badMult: 1.5, unknownSurfaceMult: 1.1,
	},
}

type surfaceClass int

const (
	surfaceUnknown surfaceClass = iota // zero value: also what a map miss returns
	surfaceGood
	surfaceFair
	surfaceBad
)

// OSM surface values grouped by how hard they are to roll over. Any value not listed here
// (including typos) falls through to surfaceUnknown.
var surfaces = map[string]surfaceClass{
	"asphalt": surfaceGood, "concrete": surfaceGood, "concrete:plates": surfaceGood, "concrete:lanes": surfaceGood,
	"paved": surfaceGood, "paving_stones": surfaceGood, "wood": surfaceGood, "metal": surfaceGood, "chipseal": surfaceGood,
	"compacted": surfaceFair, "fine_gravel": surfaceFair, "sett": surfaceFair, "grass_paver": surfaceFair, "cobblestone": surfaceFair,
	"gravel": surfaceBad, "dirt": surfaceBad, "ground": surfaceBad, "grass": surfaceBad, "sand": surfaceBad, "rock": surfaceBad,
	"unpaved": surfaceBad, "mud": surfaceBad, "earth": surfaceBad, "pebblestone": surfaceBad, "woodchips": surfaceBad,
	"unhewn_cobblestone": surfaceBad,
}

var badSmoothness = map[string]bool{"bad": true, "very_bad": true, "horrible": true, "very_horrible": true, "impassable": true}

// Road classes: minor roads are normal to walk along; major roads mean walking next to traffic.
var minorRoads = map[string]bool{"residential": true, "service": true, "unclassified": true, "track": true}
var majorRoads = map[string]bool{"tertiary": true, "secondary": true, "primary": true}

func isRoad(highway string) bool {
	return minorRoads[highway] || majorRoads[highway] || highway == "living_street"
}

// Evaluate applies profile p's rules to edge e.
//
// It walks through the factors in a fixed order (steps, wheelchair tag, surface, smoothness,
// kerbs, road type). Each factor may block the edge, multiply its length, add a penalty, and
// lower the confidence label. Reasons are recorded so the API can explain the route.
func Evaluate(p Profile, e *graph.Edge) Result {
	pp := profileParams[p]
	r := Result{Multiplier: 1, Confidence: Verified} // start neutral, then apply factors

	// Small helpers so each rule below reads as one line.
	block := func(why string) {
		r.Blocked = true
		r.Reasons = append(r.Reasons, why)
	}
	// One factor (e.g. steps for walk_winter) can add both a multiplier and a penalty; its
	// reason is recorded once.
	addReason := func(why string) {
		if !slices.Contains(r.Reasons, why) {
			r.Reasons = append(r.Reasons, why)
		}
	}
	// m <= 1 (including the zero value of an unset param) is a no-op, so a profile that
	// doesn't care about a factor simply leaves that field out.
	mult := func(m float64, why string) {
		if m > 1 {
			r.Multiplier *= m
			addReason(why)
		}
	}
	penalty := func(m float64, why string) {
		if m > 0 {
			r.PenaltyM += m
			addReason(why)
		}
	}
	// Confidence only ever goes down: verified -> inferred -> unknown.
	downgrade := func(c Confidence) {
		if c == Unknown || (c == Inferred && r.Confidence == Verified) {
			r.Confidence = c
		}
	}

	// Factor 1: steps.
	if e.HasSteps {
		if pp.stepsBlock {
			block("steps")
		} else {
			mult(pp.stepsMult, "steps")
			penalty(pp.stepsPen, "steps")
		}
	}

	// Factor 2: an explicit wheelchair=no tag.
	if pp.wheelchairNoBlock && e.Wheelchair == "no" {
		block("tagged wheelchair=no")
	}

	// Factor 3: surface.
	switch surfaces[e.Surface] {
	case surfaceGood:
		// no change
	case surfaceFair:
		mult(pp.fairMult, "surface "+e.Surface)
	case surfaceBad:
		if pp.badBlock {
			block("surface " + e.Surface)
		} else {
			mult(pp.badMult, "surface "+e.Surface)
		}
	case surfaceUnknown:
		// Sidewalks, crossings, plazas and roads in Waterloo are effectively always paved.
		if e.Highway != "track" && (e.Footway == "sidewalk" || e.IsCrossing || e.Highway == "pedestrian" || isRoad(e.Highway)) {
			downgrade(Inferred) // assumed paved: no penalty, but say it was assumed
		} else {
			mult(pp.unknownSurfaceMult, "surface unknown")
			downgrade(Unknown)
		}
	}

	// Factor 4: smoothness (rarely tagged; only matters when present).
	switch {
	case e.Smoothness == "intermediate":
		mult(pp.smoothIntermediate, "smoothness intermediate")
	case badSmoothness[e.Smoothness]:
		if pp.smoothBadBlock {
			block("smoothness " + e.Smoothness)
		} else {
			mult(pp.smoothBadMult, "smoothness "+e.Smoothness)
		}
	}

	// Factor 5: kerbs. Only the worst kerb on the edge counts.
	switch worstKerb(e.KerbStart, e.KerbEnd) {
	case "raised":
		if pp.raisedBlock {
			block("raised kerb")
		} else {
			penalty(pp.raisedPen, "raised kerb")
		}
	case "unspecified":
		penalty(pp.kerbUnspecifiedPen, "kerb of unknown height")
	case "":
		// No kerb data at all. Only a problem on crossings, where a kerb certainly exists.
		if e.IsCrossing {
			penalty(pp.crossingNoKerbPen, "crossing without kerb data")
			downgrade(Unknown)
		}
	}

	// Factor 6: walking along a road. Same for all profiles.
	if isRoad(e.Highway) {
		switch {
		case e.Sidewalk == "yes" || e.Highway == "living_street":
			// sidewalk is part of this road, or the street is shared by design: fine
		case e.Sidewalk == "separate":
			// the sidewalk exists as its own way; nudge the router onto it (it has better data)
			mult(1.5, "road with separately mapped sidewalk")
		case majorRoads[e.Highway]:
			mult(3.0, "walking along a major road")
		default:
			mult(1.3, "road without sidewalk data")
		}
	}

	// An explicit wheelchair=yes from a mapper overrides missing-data doubts.
	if e.Wheelchair == "yes" && !r.Blocked {
		r.Confidence = Verified
	}
	return r
}

// worstKerb returns "raised", "unspecified", "ok" (lowered/flush/no), or "" if untagged.
func worstKerb(kerbs ...string) string {
	worst := ""
	// Higher rank = worse for wheels.
	rank := map[string]int{"": 0, "ok": 1, "unspecified": 2, "raised": 3}
	for _, k := range kerbs {
		var c string
		switch k {
		case "":
			continue
		case "raised":
			c = "raised"
		case "lowered", "flush", "no":
			c = "ok"
		default: // "yes", "rolled" and anything else: height not known
			c = "unspecified"
		}
		if rank[c] > rank[worst] {
			worst = c
		}
	}
	return worst
}

// Table precomputes the cost of every edge in g for profile p.
// Done once per profile at startup, so a route query only does a slice lookup per edge
// instead of re-evaluating tags millions of times.
func Table(g *graph.Graph, p Profile) []float64 {
	t := make([]float64, len(g.Edges))
	for i := range g.Edges {
		e := &g.Edges[i]
		t[i] = Evaluate(p, e).Cost(e.LengthM)
	}
	return t
}
