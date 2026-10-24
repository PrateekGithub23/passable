// Package graph holds the immutable in-memory pedestrian graph: nodes, undirected edges,
// and a compact (CSR) adjacency index.
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

// Other returns the endpoint of e that is not n.
func (e *Edge) Other(n int32) int32 {
	if e.From == n {
		return e.To
	}
	return e.From
}

// Graph is read-only after New returns and safe for concurrent use.
type Graph struct {
	Nodes []Node
	Edges []Edge

	adjStart []int32 // adjEdge[adjStart[n]:adjStart[n+1]] are the edges incident to node n
	adjEdge  []int32
	byOSM    map[int64]int32
	byEdgeID map[int64]int32
}

// New builds the adjacency index. Edge From/To must be valid indices into nodes.
func New(nodes []Node, edges []Edge) *Graph {
	g := &Graph{
		Nodes:    nodes,
		Edges:    edges,
		adjStart: make([]int32, len(nodes)+1),
		adjEdge:  make([]int32, 2*len(edges)),
		byOSM:    make(map[int64]int32, len(nodes)),
		byEdgeID: make(map[int64]int32, len(edges)),
	}
	for i, n := range nodes {
		g.byOSM[n.OSMID] = int32(i)
	}
	for _, e := range edges {
		g.adjStart[e.From+1]++
		g.adjStart[e.To+1]++
	}
	for i := 1; i < len(g.adjStart); i++ {
		g.adjStart[i] += g.adjStart[i-1]
	}
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
func (g *Graph) Components(pass func(e int32) bool) (comp []int32, sizes []int) {
	comp = make([]int32, len(g.Nodes))
	for i := range comp {
		comp[i] = -1
	}
	var stack []int32
	for s := range g.Nodes {
		if comp[s] >= 0 {
			continue
		}
		id := int32(len(sizes))
		sizes = append(sizes, 0)
		comp[s] = id
		stack = append(stack[:0], int32(s))
		for len(stack) > 0 {
			u := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			sizes[id]++
			for _, ei := range g.Incident(u) {
				if pass != nil && !pass(ei) {
					continue
				}
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
