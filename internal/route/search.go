// Package route implements shortest-path search over a graph.Graph.
package route

import (
	"errors"
	"math"

	"github.com/PrateekGithub23/passable/internal/geo"
	"github.com/PrateekGithub23/passable/internal/graph"
)

// ErrNoPath means the target is unreachable under the given costs.
var ErrNoPath = errors.New("route: no path")

// CostFunc returns the cost of traversing edge e (an index into g.Edges). +Inf blocks the
// edge. For AStar to be exact, every cost must be >= the edge's length in meters.
type CostFunc func(e int32) float64

// Endpoint is a location partway along an edge: Frac of its length from the edge's From node.
type Endpoint struct {
	Edge     int32
	Frac     float64
	Lat, Lon float64
}

// Path is a search result. Edges[i] connects Nodes[i] and Nodes[i+1]. For point-to-point
// searches the partial pieces of the start and end edges are included in Cost and LengthM but
// not in Edges; Nodes is empty when the cheapest path stays on the shared start/end edge.
type Path struct {
	Nodes    []int32
	Edges    []int32
	Cost     float64
	LengthM  float64
	Expanded int // nodes popped and settled
}

// AStar finds the cheapest node-to-node path using a haversine heuristic, which is admissible
// because edge lengths are haversine sums along the geometry and costs never undercut length.
func AStar(g *graph.Graph, cost CostFunc, src, dst int32) (Path, error) {
	t := g.Nodes[dst]
	return search(g, cost, []seed{{src, 0}}, []seed{{dst, 0}}, func(n int32) float64 {
		p := g.Nodes[n]
		return geo.Haversine(p.Lat, p.Lon, t.Lat, t.Lon)
	})
}

// Dijkstra is the uninformed baseline, used to check AStar.
func Dijkstra(g *graph.Graph, cost CostFunc, src, dst int32) (Path, error) {
	return search(g, cost, []seed{{src, 0}}, []seed{{dst, 0}}, zeroH)
}

// AStarPoints finds the cheapest path between two points snapped onto edges. A partial edge
// costs the same fraction of the edge's cost as of its length.
func AStarPoints(g *graph.Graph, cost CostFunc, a, b Endpoint) (Path, error) {
	return points(g, cost, a, b, func(n int32) float64 {
		p := g.Nodes[n]
		return geo.Haversine(p.Lat, p.Lon, b.Lat, b.Lon)
	})
}

func points(g *graph.Graph, cost CostFunc, a, b Endpoint, h func(int32) float64) (Path, error) {
	ca, cb := cost(a.Edge), cost(b.Edge)
	if math.IsInf(ca, 1) || math.IsInf(cb, 1) {
		return Path{}, ErrNoPath
	}
	ea, eb := &g.Edges[a.Edge], &g.Edges[b.Edge]
	p, err := search(g, cost,
		[]seed{{ea.From, a.Frac * ca}, {ea.To, (1 - a.Frac) * ca}},
		[]seed{{eb.From, b.Frac * cb}, {eb.To, (1 - b.Frac) * cb}}, h)

	if a.Edge == b.Edge {
		span := math.Abs(a.Frac - b.Frac)
		if err != nil || span*ca <= p.Cost {
			return Path{Cost: span * ca, LengthM: span * ea.LengthM, Expanded: p.Expanded}, nil
		}
	}
	if err != nil {
		return p, err
	}
	p.LengthM += partial(ea, a.Frac, p.Nodes[0]) + partial(eb, b.Frac, p.Nodes[len(p.Nodes)-1])
	return p, nil
}

// partial is the length from the point at frac to node n, one of e's endpoints.
func partial(e *graph.Edge, frac float64, n int32) float64 {
	if n == e.From {
		return frac * e.LengthM
	}
	return (1 - frac) * e.LengthM
}

func zeroH(int32) float64 { return 0 }

type seed struct {
	node int32
	cost float64
}

// search runs A* from several seeded start nodes to a virtual target reachable from each
// target node at the given extra cost. h must be consistent and <= each target's extra cost.
func search(g *graph.Graph, cost CostFunc, seeds, targets []seed, h func(int32) float64) (Path, error) {
	n := len(g.Nodes)
	virt := int32(n)
	dist := make([]float64, n+1)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	prevEdge := make([]int32, n)
	for i := range prevEdge {
		prevEdge[i] = -1
	}
	settled := make([]bool, n+1)
	last := int32(-1) // target node through which the virtual target was best reached

	var pq minHeap
	for _, s := range seeds {
		if s.cost < dist[s.node] {
			dist[s.node] = s.cost
			pq.push(item{node: s.node, f: s.cost + h(s.node)})
		}
	}
	expanded := 0
	for len(pq) > 0 {
		cur := pq.pop()
		u := cur.node
		if settled[u] {
			continue // stale entry (lazy deletion)
		}
		settled[u] = true
		if u == virt {
			return buildPath(g, prevEdge, last, dist[virt], expanded), nil
		}
		expanded++
		for _, t := range targets {
			if t.node == u {
				if d := dist[u] + t.cost; d < dist[virt] {
					dist[virt] = d
					last = u
					pq.push(item{node: virt, f: d})
				}
			}
		}
		for _, ei := range g.Incident(u) {
			c := cost(ei)
			if math.IsInf(c, 1) {
				continue
			}
			v := g.Edges[ei].Other(u)
			if settled[v] {
				continue
			}
			if d := dist[u] + c; d < dist[v] {
				dist[v] = d
				prevEdge[v] = ei
				pq.push(item{node: v, f: d + h(v)})
			}
		}
	}
	return Path{Expanded: expanded}, ErrNoPath
}

func buildPath(g *graph.Graph, prevEdge []int32, last int32, total float64, expanded int) Path {
	p := Path{Cost: total, Expanded: expanded}
	v := last
	for prevEdge[v] >= 0 {
		ei := prevEdge[v]
		p.Edges = append(p.Edges, ei)
		p.Nodes = append(p.Nodes, v)
		p.LengthM += g.Edges[ei].LengthM
		v = g.Edges[ei].Other(v)
	}
	p.Nodes = append(p.Nodes, v)
	reverse(p.Nodes)
	reverse(p.Edges)
	return p
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

type item struct {
	node int32
	f    float64
}

// minHeap is a typed binary heap keyed on f (avoids container/heap's interface boxing).
type minHeap []item

func (h *minHeap) push(it item) {
	*h = append(*h, it)
	s := *h
	for i := len(s) - 1; i > 0; {
		p := (i - 1) / 2
		if s[p].f <= s[i].f {
			break
		}
		s[p], s[i] = s[i], s[p]
		i = p
	}
}

func (h *minHeap) pop() item {
	s := *h
	top := s[0]
	last := len(s) - 1
	s[0] = s[last]
	s = s[:last]
	for i := 0; ; {
		l, r, m := 2*i+1, 2*i+2, i
		if l < len(s) && s[l].f < s[m].f {
			m = l
		}
		if r < len(s) && s[r].f < s[m].f {
			m = r
		}
		if m == i {
			break
		}
		s[m], s[i] = s[i], s[m]
		i = m
	}
	*h = s
	return top
}
