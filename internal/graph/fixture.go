package graph

import (
	"compress/gzip"
	"encoding/json"
	"io"
)

// Fixtures: a compact JSON+gzip snapshot of a graph, so tests and benchmarks can use the real
// campus network without a database (e.g. in CI). Geometry and names are left out: golden
// routes, property tests and benchmarks only need topology, lengths and the cost-model tags.

type fixture struct {
	Nodes [][3]float64  `json:"nodes"` // [osm id, lat, lon]
	Edges []fixtureEdge `json:"edges"`
}

// Short JSON keys keep the committed file small (~21k edges).
type fixtureEdge struct {
	ID         int64   `json:"id"`
	From       int32   `json:"f"`
	To         int32   `json:"t"`
	LengthM    float64 `json:"l"`
	Highway    string  `json:"hw"`
	Footway    string  `json:"fw,omitempty"`
	Surface    string  `json:"sf,omitempty"`
	Smoothness string  `json:"sm,omitempty"`
	Incline    string  `json:"in,omitempty"`
	Wheelchair string  `json:"wc,omitempty"`
	KerbStart  string  `json:"ks,omitempty"`
	KerbEnd    string  `json:"ke,omitempty"`
	Sidewalk   string  `json:"sw,omitempty"`
	WidthM     float64 `json:"w,omitempty"`
	HasSteps   bool    `json:"st,omitempty"`
	IsCrossing bool    `json:"cr,omitempty"`
}

// WriteFixture writes g as gzipped JSON.
func WriteFixture(w io.Writer, g *Graph) error {
	f := fixture{Nodes: make([][3]float64, len(g.Nodes)), Edges: make([]fixtureEdge, len(g.Edges))}
	for i, n := range g.Nodes {
		f.Nodes[i] = [3]float64{float64(n.OSMID), n.Lat, n.Lon} // OSM ids < 2^53: exact as float64
	}
	for i, e := range g.Edges {
		f.Edges[i] = fixtureEdge{e.ID, e.From, e.To, e.LengthM, e.Highway, e.Footway, e.Surface, e.Smoothness,
			e.Incline, e.Wheelchair, e.KerbStart, e.KerbEnd, e.Sidewalk, e.WidthM, e.HasSteps, e.IsCrossing}
	}
	zw := gzip.NewWriter(w)
	if err := json.NewEncoder(zw).Encode(f); err != nil {
		return err
	}
	return zw.Close()
}

// ReadFixture reads a graph written by WriteFixture.
func ReadFixture(r io.Reader) (*Graph, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var f fixture
	if err := json.NewDecoder(zr).Decode(&f); err != nil {
		return nil, err
	}
	nodes := make([]Node, len(f.Nodes))
	for i, n := range f.Nodes {
		nodes[i] = Node{OSMID: int64(n[0]), Lat: n[1], Lon: n[2]}
	}
	edges := make([]Edge, len(f.Edges))
	for i, e := range f.Edges {
		edges[i] = Edge{ID: e.ID, From: e.From, To: e.To, LengthM: e.LengthM, Highway: e.Highway, Footway: e.Footway,
			Surface: e.Surface, Smoothness: e.Smoothness, Incline: e.Incline, Wheelchair: e.Wheelchair,
			KerbStart: e.KerbStart, KerbEnd: e.KerbEnd, Sidewalk: e.Sidewalk, WidthM: e.WidthM,
			HasSteps: e.HasSteps, IsCrossing: e.IsCrossing}
	}
	return New(nodes, edges), nil
}
