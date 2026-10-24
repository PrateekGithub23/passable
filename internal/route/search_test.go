package route

import (
	"errors"
	"math"
	"math/rand"
	"testing"

	"github.com/PrateekGithub23/passable/internal/geo"
	"github.com/PrateekGithub23/passable/internal/graph"
)

// randomGraph scatters n nodes over the project bbox and links each to its k nearest
// neighbours. Lengths are the straight-line distance times a factor >= 1, as with real
// edge geometry.
func randomGraph(r *rand.Rand, n, k int) *graph.Graph {
	nodes := make([]graph.Node, n)
	for i := range nodes {
		nodes[i] = graph.Node{OSMID: int64(i), Lat: 43.455 + 0.03*r.Float64(), Lon: -80.565 + 0.06*r.Float64()}
	}
	type pair struct{ a, b int }
	seen := map[pair]bool{}
	var edges []graph.Edge
	for i := range nodes {
		// Partial selection of the k nearest by brute force; n is small.
		type cand struct {
			j int
			d float64
		}
		var cs []cand
		for j := range nodes {
			if j != i {
				cs = append(cs, cand{j, dist(nodes[i], nodes[j])})
			}
		}
		for m := 0; m < k && m < len(cs); m++ {
			best := m
			for q := m + 1; q < len(cs); q++ {
				if cs[q].d < cs[best].d {
					best = q
				}
			}
			cs[m], cs[best] = cs[best], cs[m]
			a, b := min(i, cs[m].j), max(i, cs[m].j)
			if seen[pair{a, b}] {
				continue
			}
			seen[pair{a, b}] = true
			edges = append(edges, graph.Edge{
				ID: int64(len(edges) + 1), From: int32(a), To: int32(b),
				LengthM: cs[m].d * (1 + 0.3*r.Float64()),
			})
		}
	}
	return graph.New(nodes, edges)
}

func dist(a, b graph.Node) float64 { return geo.Haversine(a.Lat, a.Lon, b.Lat, b.Lon) }

// randomCosts multiplies each length by [1,4) and blocks about 10% of edges.
func randomCosts(r *rand.Rand, g *graph.Graph) CostFunc {
	c := make([]float64, len(g.Edges))
	for i, e := range g.Edges {
		if r.Float64() < 0.1 {
			c[i] = math.Inf(1)
		} else {
			c[i] = e.LengthM * (1 + 3*r.Float64())
		}
	}
	return func(e int32) float64 { return c[e] }
}

func TestAStarMatchesDijkstra(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	queries, noPath := 0, 0
	for trial := 0; trial < 20; trial++ {
		g := randomGraph(r, 300, 3)
		cost := randomCosts(r, g)
		for q := 0; q < 50; q++ {
			src, dst := int32(r.Intn(len(g.Nodes))), int32(r.Intn(len(g.Nodes)))
			a, errA := AStar(g, cost, src, dst)
			d, errD := Dijkstra(g, cost, src, dst)
			queries++
			if errors.Is(errA, ErrNoPath) || errors.Is(errD, ErrNoPath) {
				if errA != errD {
					t.Fatalf("trial %d %d->%d: AStar err=%v, Dijkstra err=%v", trial, src, dst, errA, errD)
				}
				noPath++
				continue
			}
			if math.Abs(a.Cost-d.Cost) > 1e-6*math.Max(1, d.Cost) {
				t.Fatalf("trial %d %d->%d: AStar cost %.6f != Dijkstra %.6f", trial, src, dst, a.Cost, d.Cost)
			}
			checkPath(t, g, cost, a, src, dst)
			if a.Expanded > d.Expanded {
				t.Errorf("trial %d %d->%d: AStar expanded %d > Dijkstra %d", trial, src, dst, a.Expanded, d.Expanded)
			}
		}
	}
	t.Logf("%d queries, %d unreachable", queries, noPath)
}

// checkPath verifies the path is connected, runs src->dst, and its cost and length add up.
func checkPath(t *testing.T, g *graph.Graph, cost CostFunc, p Path, src, dst int32) {
	t.Helper()
	if p.Nodes[0] != src || p.Nodes[len(p.Nodes)-1] != dst || len(p.Edges) != len(p.Nodes)-1 {
		t.Fatalf("malformed path %+v", p)
	}
	var c, l float64
	for i, ei := range p.Edges {
		e := g.Edges[ei]
		if !(e.From == p.Nodes[i] && e.To == p.Nodes[i+1]) && !(e.To == p.Nodes[i] && e.From == p.Nodes[i+1]) {
			t.Fatalf("edge %d does not join nodes %d,%d", ei, p.Nodes[i], p.Nodes[i+1])
		}
		c += cost(ei)
		l += e.LengthM
	}
	if math.Abs(c-p.Cost) > 1e-6*math.Max(1, c) || math.Abs(l-p.LengthM) > 1e-6*math.Max(1, l) {
		t.Fatalf("cost/length mismatch: summed %.3f/%.3f, reported %.3f/%.3f", c, l, p.Cost, p.LengthM)
	}
}

func TestSameNodeAndBlocked(t *testing.T) {
	// 0 -- 1 -- 2, with 1-2 blocked.
	g := graph.New(
		[]graph.Node{{OSMID: 10, Lat: 43.46, Lon: -80.54}, {OSMID: 11, Lat: 43.461, Lon: -80.54}, {OSMID: 12, Lat: 43.462, Lon: -80.54}},
		[]graph.Edge{{ID: 1, From: 0, To: 1, LengthM: 111}, {ID: 2, From: 1, To: 2, LengthM: 111}},
	)
	cost := func(e int32) float64 {
		if e == 1 {
			return math.Inf(1)
		}
		return g.Edges[e].LengthM
	}
	p, err := AStar(g, cost, 0, 0)
	if err != nil || p.Cost != 0 || len(p.Edges) != 0 {
		t.Fatalf("src==dst: got %+v, %v", p, err)
	}
	if _, err := AStar(g, cost, 0, 2); !errors.Is(err, ErrNoPath) {
		t.Fatalf("blocked: want ErrNoPath, got %v", err)
	}
	p, err = AStar(g, cost, 2, 1) // src side of the blocked edge has no other exits
	if !errors.Is(err, ErrNoPath) {
		t.Fatalf("blocked reverse: want ErrNoPath, got %+v %v", p, err)
	}
}

// TestAStarPointsMatchesDijkstra checks point-to-point search, including the shared-edge case.
func TestAStarPointsMatchesDijkstra(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	sameEdge := 0
	for trial := 0; trial < 20; trial++ {
		g := randomGraph(r, 300, 3)
		cost := randomCosts(r, g)
		randomPoint := func() Endpoint {
			ei := int32(r.Intn(len(g.Edges)))
			e, f := g.Edges[ei], r.Float64()
			a, b := g.Nodes[e.From], g.Nodes[e.To]
			return Endpoint{Edge: ei, Frac: f, Lat: a.Lat + f*(b.Lat-a.Lat), Lon: a.Lon + f*(b.Lon-a.Lon)}
		}
		for q := 0; q < 50; q++ {
			a, b := randomPoint(), randomPoint()
			if q%10 == 0 {
				b.Edge, b.Frac = a.Edge, r.Float64()
				sameEdge++
			}
			pa, errA := AStarPoints(g, cost, a, b)
			pd, errD := points(g, cost, a, b, zeroH)
			if (errA == nil) != (errD == nil) {
				t.Fatalf("trial %d: AStar err=%v, Dijkstra err=%v", trial, errA, errD)
			}
			if errA != nil {
				continue
			}
			if math.Abs(pa.Cost-pd.Cost) > 1e-6*math.Max(1, pd.Cost) {
				t.Fatalf("trial %d: AStar cost %.6f != Dijkstra %.6f", trial, pa.Cost, pd.Cost)
			}
			if pa.LengthM <= 0 && a.Frac != b.Frac {
				t.Fatalf("trial %d: non-positive length %+v", trial, pa)
			}
		}
	}
	t.Logf("%d shared-edge queries", sameEdge)
}

func TestAStarPointsSameEdgeDirect(t *testing.T) {
	g := graph.New(
		[]graph.Node{{OSMID: 1, Lat: 43.46, Lon: -80.54}, {OSMID: 2, Lat: 43.461, Lon: -80.54}},
		[]graph.Edge{{ID: 1, From: 0, To: 1, LengthM: 111.2}},
	)
	cost := func(e int32) float64 { return 2 * g.Edges[e].LengthM }
	p, err := AStarPoints(g, cost, Endpoint{Edge: 0, Frac: 0.2, Lat: 43.4602, Lon: -80.54}, Endpoint{Edge: 0, Frac: 0.7, Lat: 43.4607, Lon: -80.54})
	if err != nil || len(p.Nodes) != 0 || math.Abs(p.LengthM-0.5*111.2) > 1e-9 || math.Abs(p.Cost-111.2) > 1e-9 {
		t.Fatalf("got %+v, %v", p, err)
	}
}
