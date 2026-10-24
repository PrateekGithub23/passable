// Package route implements shortest-path search over a graph.Graph.
//
// The core is one function, search(), which is A* generalised to:
//   - several start nodes, each with an initial cost ("seeds"), and
//   - several target nodes, each with an extra cost to finish ("targets").
//
// That generalisation lets a route start and end partway along an edge: a clicked point in
// the middle of an edge is reachable from both of its endpoints at a fraction of the edge's
// cost. Plain node-to-node A* and Dijkstra are the special case of one seed and one target.
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
//
// Admissible = the heuristic never overestimates the remaining cost. The straight-line
// distance to the target can't be more than any real path's length, and cost >= length, so
// A* still returns the optimal route while exploring far fewer nodes than Dijkstra.
func AStar(g *graph.Graph, cost CostFunc, src, dst int32) (Path, error) {
	t := g.Nodes[dst]
	return search(g, cost, []seed{{src, 0}}, []seed{{dst, 0}}, func(n int32) float64 {
		p := g.Nodes[n]
		return geo.Haversine(p.Lat, p.Lon, t.Lat, t.Lon)
	})
}

// Dijkstra is the uninformed baseline, used to check AStar.
// It is the same search with a heuristic of 0 (no guess about the remaining distance).
func Dijkstra(g *graph.Graph, cost CostFunc, src, dst int32) (Path, error) {
	return search(g, cost, []seed{{src, 0}}, []seed{{dst, 0}}, zeroH)
}

// AStarPoints finds the cheapest path between two points snapped onto edges. A partial edge
// costs the same fraction of the edge's cost as of its length.
func AStarPoints(g *graph.Graph, cost CostFunc, a, b Endpoint) (Path, error) {
	return points(g, cost, a, b, func(n int32) float64 {
		// Heuristic: straight-line distance to the destination point itself.
		p := g.Nodes[n]
		return geo.Haversine(p.Lat, p.Lon, b.Lat, b.Lon)
	})
}

// points is AStarPoints with a pluggable heuristic (tests pass zeroH to get Dijkstra).
func points(g *graph.Graph, cost CostFunc, a, b Endpoint, h func(int32) float64) (Path, error) {
	ca, cb := cost(a.Edge), cost(b.Edge)
	// Starting or ending on a blocked edge is impossible. (Checked up front also because
	// 0 * Inf is NaN, which would poison the arithmetic below.)
	if math.IsInf(ca, 1) || math.IsInf(cb, 1) {
		return Path{}, ErrNoPath
	}
	ea, eb := &g.Edges[a.Edge], &g.Edges[b.Edge]
	// From the start point you can walk to either end of its edge:
	//   to From costs Frac of the edge, to To costs (1 - Frac).
	// Symmetrically, the destination point can be reached from either end of its edge.
	p, err := search(g, cost,
		[]seed{{ea.From, a.Frac * ca}, {ea.To, (1 - a.Frac) * ca}},
		[]seed{{eb.From, b.Frac * cb}, {eb.To, (1 - b.Frac) * cb}}, h)

	// Special case: both points on the same edge. Walking straight along the edge between
	// them never visits a node, so the search can't see it; compare it explicitly.
	if a.Edge == b.Edge {
		span := math.Abs(a.Frac - b.Frac)
		if err != nil || span*ca <= p.Cost {
			return Path{Cost: span * ca, LengthM: span * ea.LengthM, Expanded: p.Expanded}, nil
		}
	}
	if err != nil {
		return p, err
	}
	// search() only counted full edges; add the two partial pieces at the ends.
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

// seed is a node with a cost: a starting cost for seeds, an extra finishing cost for targets.
type seed struct {
	node int32
	cost float64
}

// search runs A* from several seeded start nodes to a virtual target reachable from each
// target node at the given extra cost. h must be consistent and <= each target's extra cost.
//
// The "virtual target" is an extra node (index n, one past the real nodes) that every target
// node connects to with its finishing cost. Searching for that single node finds the best of
// all targets at once. We stop when the virtual target is popped from the queue: at that
// moment its cost is final, because every other queued entry has a larger-or-equal f.
func search(g *graph.Graph, cost CostFunc, seeds, targets []seed, h func(int32) float64) (Path, error) {
	n := len(g.Nodes)
	virt := int32(n)

	// Per-node search state, in flat arrays indexed by node (fast; no maps).
	// dist[v] = cheapest cost found so far from the start to v ("g" in A* terms).
	dist := make([]float64, n+1)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	// prevEdge[v] = the edge we arrived at v through, for rebuilding the path. -1 marks a seed.
	prevEdge := make([]int32, n)
	for i := range prevEdge {
		prevEdge[i] = -1
	}
	// settled[v] = v's dist is final (it has been popped once).
	settled := make([]bool, n+1)
	last := int32(-1) // target node through which the virtual target was best reached

	// Priority queue ordered by f = dist + h: "cost so far + optimistic guess of cost to go".
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
		// The heap may hold several entries for one node (we push again whenever we find a
		// cheaper way, rather than updating in place). Only the first pop counts.
		if settled[u] {
			continue // stale entry (lazy deletion)
		}
		settled[u] = true
		if u == virt {
			// Reached the virtual target: the cheapest route to any destination is known.
			return buildPath(g, prevEdge, last, dist[virt], expanded), nil
		}
		expanded++
		// If u is one of the target nodes, offer the virtual target a route through u.
		for _, t := range targets {
			if t.node == u {
				if d := dist[u] + t.cost; d < dist[virt] {
					dist[virt] = d
					last = u
					pq.push(item{node: virt, f: d}) // h(virtual target) = 0
				}
			}
		}
		// Relax every edge out of u: if going through u is cheaper than the best known way
		// to the neighbour v, record it and queue v.
		for _, ei := range g.Incident(u) {
			c := cost(ei)
			if math.IsInf(c, 1) {
				continue // blocked for this profile
			}
			v := g.Edges[ei].Other(u)
			if settled[v] {
				continue // v's cost is already final; can't improve it
			}
			if d := dist[u] + c; d < dist[v] {
				dist[v] = d
				prevEdge[v] = ei
				pq.push(item{node: v, f: d + h(v)})
			}
		}
	}
	// Queue ran dry without reaching a target: everything reachable was explored.
	return Path{Expanded: expanded}, ErrNoPath
}

// buildPath walks prevEdge backwards from the last real node to a seed, then reverses the
// result so it reads start -> end.
func buildPath(g *graph.Graph, prevEdge []int32, last int32, total float64, expanded int) Path {
	p := Path{Cost: total, Expanded: expanded}
	v := last
	for prevEdge[v] >= 0 { // stop at a seed node (prevEdge == -1)
		ei := prevEdge[v]
		p.Edges = append(p.Edges, ei)
		p.Nodes = append(p.Nodes, v)
		p.LengthM += g.Edges[ei].LengthM
		v = g.Edges[ei].Other(v) // step back across the edge
	}
	p.Nodes = append(p.Nodes, v) // the seed node the path started from
	reverse(p.Nodes)
	reverse(p.Edges)
	return p
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// item is one priority-queue entry.
type item struct {
	node int32
	f    float64
}

// minHeap is a typed binary heap keyed on f (avoids container/heap's interface boxing).
// Layout: s[0] is the smallest; the children of s[i] are s[2i+1] and s[2i+2].
type minHeap []item

// push appends at the bottom and "sifts up": swap with the parent while smaller than it.
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

// pop removes the smallest item: move the last item to the root, then "sift down": swap with
// the smaller child while larger than it.
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
			break // heap order restored
		}
		s[m], s[i] = s[i], s[m]
		i = m
	}
	*h = s
	return top
}
