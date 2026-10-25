package route

// Tests and benchmarks on the REAL campus graph, loaded from the committed fixture
// (testdata/campus-graph.json.gz, written by cmd/exportgraph). No database needed, so they
// run in CI.

import (
	"errors"
	"math"
	"math/rand"
	"os"
	"slices"
	"testing"

	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/geo"
	"github.com/PrateekGithub23/passable/internal/graph"
)

var campus *graph.Graph // loaded once, shared by tests and benchmarks (read-only)

func loadCampus(tb testing.TB) *graph.Graph {
	tb.Helper()
	if campus == nil {
		f, err := os.Open("../../testdata/campus-graph.json.gz")
		if err != nil {
			tb.Fatalf("open fixture: %v", err)
		}
		defer f.Close()
		if campus, err = graph.ReadFixture(f); err != nil {
			tb.Fatalf("read fixture: %v", err)
		}
	}
	return campus
}

// profileNet is a profile's cost table plus the nodes of its main usable network.
type profileNet struct {
	table []float64
	comp  []int32
	main  int32
	pool  []int32 // node indices in the main network
}

func netFor(g *graph.Graph, p cost.Profile) profileNet {
	n := profileNet{table: cost.Table(g, p)}
	var sizes []int
	n.comp, sizes = g.Components(func(e int32) bool { return !math.IsInf(n.table[e], 1) })
	n.main = int32(slices.Index(sizes, slices.Max(sizes)))
	for i, c := range n.comp {
		if c == n.main {
			n.pool = append(n.pool, int32(i))
		}
	}
	return n
}

func (n profileNet) cost(e int32) float64 { return n.table[e] }

// nearest is the node in the profile's main network closest to (lat, lon); the same snapping
// the cmd/route CLI uses, so these goldens match the Day 2 CLI output.
func (n profileNet) nearest(g *graph.Graph, lat, lon float64) int32 {
	best, bestD := int32(-1), math.Inf(1)
	for _, i := range n.pool {
		if d := geo.Haversine(lat, lon, g.Nodes[i].Lat, g.Nodes[i].Lon); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// TestGoldenCampusRoutes pins down routing behaviour on real campus pairs. The expected values
// were recorded on Day 2 (docs/benchmarks/day2.md). If the cost model or data changes on
// purpose, update them here in the same commit, so the change is visible in review.
func TestGoldenCampusRoutes(t *testing.T) {
	g := loadCampus(t)
	type want struct {
		lengthM float64
		steps   bool // does the route use any steps?
	}
	pairs := []struct {
		name     string
		from, to [2]float64 // lat, lon
		want     map[cost.Profile]want
	}{
		{"Dana Porter -> E7", [2]float64{43.4697, -80.5421}, [2]float64{43.4730, -80.5395},
			map[cost.Profile]want{cost.Wheelchair: {794, false}, cost.Stroller: {777, false}, cost.WalkWinter: {664, true}}},
		{"SLC -> Hagey Hall", [2]float64{43.4718, -80.5452}, [2]float64{43.4685, -80.5418},
			map[cost.Profile]want{cost.Wheelchair: {569, false}, cost.Stroller: {569, false}, cost.WalkWinter: {611, true}}},
		{"V1 -> MC", [2]float64{43.4710, -80.5503}, [2]float64{43.4721, -80.5440},
			map[cost.Profile]want{cost.Wheelchair: {646, false}, cost.Stroller: {646, false}, cost.WalkWinter: {562, true}}},
	}
	for _, p := range cost.Profiles {
		net := netFor(g, p)
		for _, pair := range pairs {
			src := net.nearest(g, pair.from[0], pair.from[1])
			dst := net.nearest(g, pair.to[0], pair.to[1])
			path, err := AStar(g, net.cost, src, dst)
			if err != nil {
				t.Errorf("%s / %s: %v", pair.name, p, err)
				continue
			}
			usesSteps := false
			for _, ei := range path.Edges {
				usesSteps = usesSteps || g.Edges[ei].HasSteps
			}
			w := pair.want[p]
			// Day 2 recorded lengths rounded to whole metres, hence the 1 m tolerance.
			if math.Abs(path.LengthM-w.lengthM) > 1 || usesSteps != w.steps {
				t.Errorf("%s / %s: %.1f m (steps %v), want %.0f m (steps %v)", pair.name, p, path.LengthM, usesSteps, w.lengthM, w.steps)
			}
			// Hard rule, regardless of the numbers: wheelchair and stroller never use steps.
			if p != cost.WalkWinter && usesSteps {
				t.Errorf("%s / %s: route uses steps", pair.name, p)
			}
		}
	}
}

// TestAStarMatchesDijkstraOnCampus is the property test on real data: for random pairs in each
// profile's network, A* (with the haversine heuristic) returns exactly Dijkstra's cost.
func TestAStarMatchesDijkstraOnCampus(t *testing.T) {
	g := loadCampus(t)
	r := rand.New(rand.NewSource(8))
	for _, p := range cost.Profiles {
		net := netFor(g, p)
		for q := 0; q < 150; q++ {
			src, dst := net.pool[r.Intn(len(net.pool))], net.pool[r.Intn(len(net.pool))]
			a, errA := AStar(g, net.cost, src, dst)
			d, errD := Dijkstra(g, net.cost, src, dst)
			if errA != nil || errD != nil {
				t.Fatalf("%s %d->%d: errors %v / %v (both nodes are in the main network)", p, src, dst, errA, errD)
			}
			if math.Abs(a.Cost-d.Cost) > 1e-6*math.Max(1, d.Cost) {
				t.Fatalf("%s %d->%d: A* %.6f != Dijkstra %.6f", p, src, dst, a.Cost, d.Cost)
			}
			if a.Expanded > d.Expanded {
				t.Errorf("%s %d->%d: A* expanded %d > Dijkstra %d", p, src, dst, a.Expanded, d.Expanded)
			}
		}
	}
}

// BenchmarkAStarCampus: one A* search between random nodes of the real graph, per profile.
// go test -bench AStarCampus -benchmem ./internal/route
func BenchmarkAStarCampus(b *testing.B) {
	g := loadCampus(b)
	for _, p := range cost.Profiles {
		net := netFor(g, p)
		b.Run(string(p), func(b *testing.B) {
			r := rand.New(rand.NewSource(42))
			expanded := 0
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				src, dst := net.pool[r.Intn(len(net.pool))], net.pool[r.Intn(len(net.pool))]
				path, err := AStar(g, net.cost, src, dst)
				if err != nil && !errors.Is(err, ErrNoPath) {
					b.Fatal(err)
				}
				expanded += path.Expanded
			}
			b.ReportMetric(float64(expanded)/float64(b.N), "nodes/op")
		})
	}
}
