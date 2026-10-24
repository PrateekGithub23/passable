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

// Path is a search result. Edges[i] connects Nodes[i] and Nodes[i+1].
type Path struct {
	Nodes    []int32
	Edges    []int32
	Cost     float64
	LengthM  float64
	Expanded int // nodes popped and settled
}

// AStar finds the cheapest path using a haversine heuristic, which is admissible because
// edge lengths are haversine sums along the geometry and costs never undercut length.
func AStar(g *graph.Graph, cost CostFunc, src, dst int32) (Path, error) {
	t := g.Nodes[dst]
	return search(g, cost, src, dst, func(n int32) float64 {
		p := g.Nodes[n]
		return geo.Haversine(p.Lat, p.Lon, t.Lat, t.Lon)
	})
}

// Dijkstra is the uninformed baseline, used to check AStar.
func Dijkstra(g *graph.Graph, cost CostFunc, src, dst int32) (Path, error) {
	return search(g, cost, src, dst, func(int32) float64 { return 0 })
}

func search(g *graph.Graph, cost CostFunc, src, dst int32, h func(int32) float64) (Path, error) {
	n := len(g.Nodes)
	dist := make([]float64, n)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	prevEdge := make([]int32, n)
	settled := make([]bool, n)

	dist[src] = 0
	pq := minHeap{{node: src, f: h(src)}}
	expanded := 0
	for len(pq) > 0 {
		cur := pq.pop()
		u := cur.node
		if settled[u] {
			continue // stale entry (lazy deletion)
		}
		settled[u] = true
		expanded++
		if u == dst {
			return buildPath(g, cost, prevEdge, src, dst, dist[dst], expanded), nil
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

func buildPath(g *graph.Graph, cost CostFunc, prevEdge []int32, src, dst int32, total float64, expanded int) Path {
	p := Path{Cost: total, Expanded: expanded}
	for v := dst; v != src; {
		ei := prevEdge[v]
		p.Edges = append(p.Edges, ei)
		p.Nodes = append(p.Nodes, v)
		p.LengthM += g.Edges[ei].LengthM
		v = g.Edges[ei].Other(v)
	}
	p.Nodes = append(p.Nodes, src)
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
