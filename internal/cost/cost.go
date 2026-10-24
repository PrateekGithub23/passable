// Package cost turns edge tags into per-profile traversal costs.
//
// cost = length_m * multiplier + penalty_m, with multiplier >= 1 and penalty >= 0, so a
// cost never undercuts the edge length and the router's haversine heuristic stays
// admissible. Blocked edges cost +Inf. Values are documented in DECISIONS.md.
package cost

import (
	"fmt"
	"math"

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
		return math.Inf(1)
	}
	return lengthM*r.Multiplier + r.PenaltyM
}

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
	WalkWinter: {
		stepsMult: 2.0, stepsPen: 15, fairMult: 1.2, badMult: 1.5, unknownSurfaceMult: 1.1,
	},
}

type surfaceClass int

const (
	surfaceUnknown surfaceClass = iota
	surfaceGood
	surfaceFair
	surfaceBad
)

var surfaces = map[string]surfaceClass{
	"asphalt": surfaceGood, "concrete": surfaceGood, "concrete:plates": surfaceGood, "concrete:lanes": surfaceGood,
	"paved": surfaceGood, "paving_stones": surfaceGood, "wood": surfaceGood, "metal": surfaceGood, "chipseal": surfaceGood,
	"compacted": surfaceFair, "fine_gravel": surfaceFair, "sett": surfaceFair, "grass_paver": surfaceFair, "cobblestone": surfaceFair,
	"gravel": surfaceBad, "dirt": surfaceBad, "ground": surfaceBad, "grass": surfaceBad, "sand": surfaceBad, "rock": surfaceBad,
	"unpaved": surfaceBad, "mud": surfaceBad, "earth": surfaceBad, "pebblestone": surfaceBad, "woodchips": surfaceBad,
	"unhewn_cobblestone": surfaceBad,
}

var badSmoothness = map[string]bool{"bad": true, "very_bad": true, "horrible": true, "very_horrible": true, "impassable": true}

var minorRoads = map[string]bool{"residential": true, "service": true, "unclassified": true, "track": true}
var majorRoads = map[string]bool{"tertiary": true, "secondary": true, "primary": true}

func isRoad(highway string) bool {
	return minorRoads[highway] || majorRoads[highway] || highway == "living_street"
}

// Evaluate applies profile p's rules to edge e.
func Evaluate(p Profile, e *graph.Edge) Result {
	pp := profileParams[p]
	r := Result{Multiplier: 1, Confidence: Verified}
	block := func(why string) {
		r.Blocked = true
		r.Reasons = append(r.Reasons, why)
	}
	mult := func(m float64, why string) {
		if m > 1 {
			r.Multiplier *= m
			r.Reasons = append(r.Reasons, why)
		}
	}
	penalty := func(m float64, why string) {
		if m > 0 {
			r.PenaltyM += m
			r.Reasons = append(r.Reasons, why)
		}
	}
	downgrade := func(c Confidence) {
		if c == Unknown || (c == Inferred && r.Confidence == Verified) {
			r.Confidence = c
		}
	}

	if e.HasSteps {
		if pp.stepsBlock {
			block("steps")
		} else {
			mult(pp.stepsMult, "steps")
			penalty(pp.stepsPen, "steps")
		}
	}

	if pp.wheelchairNoBlock && e.Wheelchair == "no" {
		block("tagged wheelchair=no")
	}

	switch surfaces[e.Surface] {
	case surfaceGood:
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
			downgrade(Inferred)
		} else {
			mult(pp.unknownSurfaceMult, "surface unknown")
			downgrade(Unknown)
		}
	}

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
		if e.IsCrossing {
			penalty(pp.crossingNoKerbPen, "crossing without kerb data")
			downgrade(Unknown)
		}
	}

	if isRoad(e.Highway) {
		switch {
		case e.Sidewalk == "yes" || e.Highway == "living_street":
		case e.Sidewalk == "separate":
			mult(1.5, "road with separately mapped sidewalk")
		case majorRoads[e.Highway]:
			mult(3.0, "walking along a major road")
		default:
			mult(1.3, "road without sidewalk data")
		}
	}

	if e.Wheelchair == "yes" && !r.Blocked {
		r.Confidence = Verified
	}
	return r
}

// worstKerb returns "raised", "unspecified", "ok" (lowered/flush/no), or "" if untagged.
func worstKerb(kerbs ...string) string {
	worst := ""
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
func Table(g *graph.Graph, p Profile) []float64 {
	t := make([]float64, len(g.Edges))
	for i := range g.Edges {
		e := &g.Edges[i]
		t[i] = Evaluate(p, e).Cost(e.LengthM)
	}
	return t
}
