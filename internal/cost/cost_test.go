package cost

import (
	"math"
	"slices"
	"testing"

	"github.com/PrateekGithub23/passable/internal/graph"
)

func TestEvaluate(t *testing.T) {
	inf := math.Inf(1)
	sidewalk := graph.Edge{Highway: "footway", Footway: "sidewalk", Surface: "concrete"}
	tests := []struct {
		name string
		edge graph.Edge
		want map[Profile]float64 // cost of a 100 m edge
		conf Confidence
	}{
		{"paved sidewalk", sidewalk,
			map[Profile]float64{Wheelchair: 100, Stroller: 100, WalkWinter: 100}, Verified},
		{"steps", graph.Edge{Highway: "steps", Surface: "concrete", HasSteps: true},
			map[Profile]float64{Wheelchair: inf, Stroller: inf, WalkWinter: 215}, Verified},
		{"gravel path", graph.Edge{Highway: "path", Surface: "gravel"},
			map[Profile]float64{Wheelchair: inf, Stroller: 200, WalkWinter: 150}, Verified},
		{"compacted path", graph.Edge{Highway: "path", Surface: "compacted"},
			map[Profile]float64{Wheelchair: 150, Stroller: 120, WalkWinter: 120}, Verified},
		{"untagged footway", graph.Edge{Highway: "footway"},
			map[Profile]float64{Wheelchair: 130, Stroller: 115, WalkWinter: 110}, Unknown},
		{"untagged sidewalk is inferred paved", graph.Edge{Highway: "footway", Footway: "sidewalk"},
			map[Profile]float64{Wheelchair: 100, Stroller: 100, WalkWinter: 100}, Inferred},
		{"crossing, lowered kerb", graph.Edge{Highway: "footway", Footway: "crossing", Surface: "asphalt", IsCrossing: true, KerbStart: "lowered"},
			map[Profile]float64{Wheelchair: 100, Stroller: 100, WalkWinter: 100}, Verified},
		{"crossing, no kerb data", graph.Edge{Highway: "footway", Footway: "crossing", Surface: "asphalt", IsCrossing: true},
			map[Profile]float64{Wheelchair: 130, Stroller: 110, WalkWinter: 100}, Unknown},
		{"raised kerb wins over lowered", graph.Edge{Highway: "footway", Surface: "asphalt", KerbStart: "lowered", KerbEnd: "raised"},
			map[Profile]float64{Wheelchair: inf, Stroller: 130, WalkWinter: 100}, Verified},
		{"kerb=yes", graph.Edge{Highway: "footway", Surface: "asphalt", KerbStart: "yes"},
			map[Profile]float64{Wheelchair: 150, Stroller: 110, WalkWinter: 100}, Verified},
		{"wheelchair=no", graph.Edge{Highway: "footway", Surface: "asphalt", Wheelchair: "no"},
			map[Profile]float64{Wheelchair: inf, Stroller: 100, WalkWinter: 100}, Verified},
		{"intermediate smoothness", graph.Edge{Highway: "footway", Surface: "asphalt", Smoothness: "intermediate"},
			map[Profile]float64{Wheelchair: 120, Stroller: 110, WalkWinter: 100}, Verified},
		{"residential, no sidewalk data", graph.Edge{Highway: "residential", Surface: "asphalt"},
			map[Profile]float64{Wheelchair: 130, Stroller: 130, WalkWinter: 130}, Verified},
		{"secondary, sidewalk separate", graph.Edge{Highway: "secondary", Sidewalk: "separate"},
			map[Profile]float64{Wheelchair: 150, Stroller: 150, WalkWinter: 150}, Inferred},
		{"secondary, no sidewalk", graph.Edge{Highway: "secondary", Surface: "asphalt", Sidewalk: "no"},
			map[Profile]float64{Wheelchair: 300, Stroller: 300, WalkWinter: 300}, Verified},
		{"residential with sidewalk", graph.Edge{Highway: "residential", Sidewalk: "yes"},
			map[Profile]float64{Wheelchair: 100, Stroller: 100, WalkWinter: 100}, Inferred},
		{"untagged track is unknown", graph.Edge{Highway: "track"},
			map[Profile]float64{Wheelchair: 130 * 1.3, Stroller: 115 * 1.3, WalkWinter: 110 * 1.3}, Unknown},
	}
	for _, tc := range tests {
		for p, want := range tc.want {
			e := tc.edge
			e.LengthM = 100
			r := Evaluate(p, &e)
			if got := r.Cost(e.LengthM); math.Abs(got-want) > 1e-9 && !(math.IsInf(got, 1) && math.IsInf(want, 1)) {
				t.Errorf("%s / %s: cost %.2f, want %.2f (reasons %v)", tc.name, p, got, want, r.Reasons)
			}
			if len(r.Reasons) != len(slices.Compact(slices.Clone(r.Reasons))) {
				t.Errorf("%s / %s: duplicate reasons %v", tc.name, p, r.Reasons)
			}
			if !r.Blocked && r.Confidence != tc.conf {
				t.Errorf("%s / %s: confidence %s, want %s", tc.name, p, r.Confidence, tc.conf)
			}
		}
	}
}

// TestCostNeverBelowLength guards A* admissibility over every combination of tag values.
func TestCostNeverBelowLength(t *testing.T) {
	highways := []string{"footway", "path", "steps", "residential", "secondary", "living_street", "track", "pedestrian"}
	surfs := []string{"", "concrete", "compacted", "gravel", "weird_value"}
	kerbs := []string{"", "lowered", "raised", "yes"}
	for _, p := range Profiles {
		for _, h := range highways {
			for _, s := range surfs {
				for _, k := range kerbs {
					for _, crossing := range []bool{false, true} {
						e := graph.Edge{Highway: h, Surface: s, KerbStart: k, IsCrossing: crossing, HasSteps: h == "steps", LengthM: 37}
						if c := Evaluate(p, &e).Cost(e.LengthM); c < e.LengthM {
							t.Fatalf("%s %+v: cost %.2f < length", p, e, c)
						}
					}
				}
			}
		}
	}
}
