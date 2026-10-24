// Package graph holds the immutable in-memory pedestrian graph: nodes, undirected edges,
// and a compact (CSR) adjacency index.
//
// Nodes and edges are addressed by their position in the Nodes/Edges slices (int32 "indices"),
// not by their database ids. Indices are dense (0..n-1), so per-node search state in the router
// can live in plain arrays instead of maps, which is much faster. The maps byOSM/byEdgeID
// translate database ids to indices at the boundaries (snapping, API responses).
package graph

// Node is a graph vertex (an edge endpoint in OSM).
type Node struct {
	OSMID    int64
	Lat, Lon float64
}

// Edge is an undirected segment between two node indices. Absent OSM tags are "".
type Edge struct {
	ID         int64 // edges.id in Postgres
	From, To   int32 // node indices
	LengthM    float64
	Highway    string
	Footway    string
	Surface    string
	Smoothness string
	Incline    string
	Wheelchair string
	KerbStart  string
	KerbEnd    string
	Sidewalk   string // normalized: "yes", "separate", "no" or ""
	WidthM     float64
	HasSteps   bool
	IsCrossing bool
	Name       string  // OSM name tag, for explanations
	Geom       []Point // ordered From -> To
}

// Other returns the endpoint of e that is not n. Because edges are undirected, the router
// uses this to step across an edge from whichever end it arrived at.
func (e *Edge) Other(n int32) int32 {
	if e.From == n {
		return e.To
	}
	return e.From
}

// Graph is read-only after New returns and safe for concurrent use.
//
// Adjacency uses CSR ("compressed sparse row"): instead of one small slice per node (thousands
// of separate allocations), all incident-edge lists are packed back to back into one array,
// adjEdge, and adjStart[n] says where node n's list begins. Example with 3 nodes:
//
//	adjStart = [0, 2, 5, 6]           node 0 -> adjEdge[0:2], node 1 -> adjEdge[2:5], node 2 -> adjEdge[5:6]
//	adjEdge  = [e0 e1 | e0 e2 e3 | e3]
//
// Two flat arrays are cache-friendly and cheap for the garbage collector.
type Graph struct {
	Nodes []Node
	Edges []Edge

	adjStart []int32 // adjEdge[adjStart[n]:adjStart[n+1]] are the edges incident to node n
	adjEdge  []int32
	byOSM    map[int64]int32 // OSM node id -> node index
	byEdgeID map[int64]int32 // Postgres edge id -> edge index
}

// New builds the adjacency index. Edge From/To must be valid indices into nodes.
func New(nodes []Node, edges []Edge) *Graph {
	g := &Graph{
		Nodes:    nodes,
		Edges:    edges,
		adjStart: make([]int32, len(nodes)+1),
		// Each undirected edge appears in two lists: its From node's and its To node's.
		adjEdge:  make([]int32, 2*len(edges)),
		byOSM:    make(map[int64]int32, len(nodes)),
		byEdgeID: make(map[int64]int32, len(edges)),
	}
	for i, n := range nodes {
		g.byOSM[n.OSMID] = int32(i)
	}

	// Step 1: count each node's degree. Counts go in slot n+1 so the prefix sum below
	// shifts them into place.
	for _, e := range edges {
		g.adjStart[e.From+1]++
		g.adjStart[e.To+1]++
	}
	// Step 2: prefix sum turns degrees into start offsets: adjStart[n] = total degree of
	// nodes 0..n-1, i.e. where node n's list begins in adjEdge.
	for i := 1; i < len(g.adjStart); i++ {
		g.adjStart[i] += g.adjStart[i-1]
	}
	// Step 3: place each edge into both endpoints' lists. fill[n] is the next free slot
	// in node n's list; it starts at the list's beginning and advances as we write.
	fill := make([]int32, len(nodes))
	copy(fill, g.adjStart[:len(nodes)])
	for i, e := range edges {
		g.byEdgeID[e.ID] = int32(i)
		g.adjEdge[fill[e.From]] = int32(i)
		fill[e.From]++
		g.adjEdge[fill[e.To]] = int32(i)
		fill[e.To]++
	}
	return g
}

// Incident returns the indices of edges touching node n. The slice must not be modified.
// It is a sub-slice of adjEdge (no copy), which is why this is cheap enough to call for every
// node the router expands.
func (g *Graph) Incident(n int32) []int32 {
	return g.adjEdge[g.adjStart[n]:g.adjStart[n+1]]
}

// NodeByOSMID returns the node index for an OSM node id.
func (g *Graph) NodeByOSMID(id int64) (int32, bool) {
	i, ok := g.byOSM[id]
	return i, ok
}

// Components labels each node with its connected-component id and returns the size of each
// component. Only edges for which pass returns true are followed; nil follows all edges.
//
// Used for snapping: with pass = "edge is usable by this profile", the largest component is
// the step-free network for a wheelchair, and a start point snapped outside it could never
// reach anything.
func (g *Graph) Components(pass func(e int32) bool) (comp []int32, sizes []int) {
	comp = make([]int32, len(g.Nodes))
	for i := range comp {
		comp[i] = -1 // -1 = not visited yet
	}
	var stack []int32
	// Every unvisited node starts a new component; a depth-first search (explicit stack
	// rather than recursion, so deep graphs can't overflow the call stack) labels everything
	// reachable from it.
	for s := range g.Nodes {
		if comp[s] >= 0 {
			continue
		}
		id := int32(len(sizes))
		sizes = append(sizes, 0)
		comp[s] = id
		stack = append(stack[:0], int32(s))
		for len(stack) > 0 {
			// Pop.
			u := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			sizes[id]++
			for _, ei := range g.Incident(u) {
				if pass != nil && !pass(ei) {
					continue // edge not traversable under this filter
				}
				// Label neighbours when pushing (not when popping) so a node is never
				// pushed twice.
				if v := g.Edges[ei].Other(u); comp[v] < 0 {
					comp[v] = id
					stack = append(stack, v)
				}
			}
		}
	}
	return comp, sizes
}

// EdgeByID returns the edge index for a Postgres edge id.
func (g *Graph) EdgeByID(id int64) (int32, bool) {
	i, ok := g.byEdgeID[id]
	return i, ok
}
