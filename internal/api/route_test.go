package api

import (
	"math"
	"net/http"
	"slices"
	"testing"

	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/geo"
	"github.com/PrateekGithub23/passable/internal/graph"
)

// lineGraph builds A - B - C - D - E heading east, ~200 m apart, where B-C is steps and the
// rest are concrete footways. For wheelchair, {A,B} and {C,D,E} are separate components and
// {C,D,E} is the larger one.
func lineGraph() *graph.Graph {
	const lat, dLon = 43.47, 0.00248 // ~200 m of longitude at this latitude, so A-B is > maxSnapM from C
	nodes := make([]graph.Node, 5)
	for i := range nodes {
		nodes[i] = graph.Node{OSMID: int64(i + 1), Lat: lat, Lon: -80.54 + float64(i)*dLon}
	}
	var edges []graph.Edge
	for i := 0; i < 4; i++ {
		a, b := nodes[i], nodes[i+1]
		e := graph.Edge{
			ID: int64(i + 1), From: int32(i), To: int32(i + 1), Highway: "footway", Surface: "concrete",
			LengthM: geo.Haversine(a.Lat, a.Lon, b.Lat, b.Lon),
			Geom:    []graph.Point{{Lat: a.Lat, Lon: a.Lon}, {Lat: b.Lat, Lon: b.Lon}},
		}
		if i == 1 {
			e.Highway, e.HasSteps, e.Name = "steps", true, "Library steps"
		}
		edges = append(edges, e)
	}
	return graph.New(nodes, edges)
}

// near makes sorted snap candidates for a point 3 m north of the given fraction of edge ei.
func near(g *graph.Graph, ei int32, frac float64) []candidate {
	e := &g.Edges[ei]
	lat := e.Geom[0].Lat + 3/111_194.93
	lon := e.Geom[0].Lon + frac*(e.Geom[1].Lon-e.Geom[0].Lon)
	var cs []candidate
	for i := range g.Edges {
		f, d, at := g.Edges[i].Project(lat, lon)
		cs = append(cs, candidate{edge: int32(i), frac: f, distM: d, at: at})
	}
	slices.SortFunc(cs, func(a, b candidate) int { return cmpFloat(a.distM, b.distM) })
	return cs
}

func TestPlanAccessible(t *testing.T) {
	g := lineGraph()
	s := New(nil, g)
	resp, status, msg := s.plan(cost.Wheelchair, near(g, 2, 0.5), near(g, 3, 0.5))
	if status != http.StatusOK || !resp.Accessible || len(resp.Barriers) != 0 {
		t.Fatalf("status %d %q, resp %+v", status, msg, resp)
	}
	if math.Abs(resp.DistanceM-g.Edges[2].LengthM) > 0.5 {
		t.Errorf("distance %.1f, want ~%.1f (half of C-D plus half of D-E)", resp.DistanceM, g.Edges[2].LengthM)
	}
	if len(resp.Segments) != 1 || resp.Segments[0].Confidence != cost.Verified || len(resp.Segments[0].EdgeIDs) != 2 {
		t.Errorf("want one merged verified segment over 2 edges, got %+v", resp.Segments)
	}
}

func TestPlanExplainsBarrier(t *testing.T) {
	g := lineGraph()
	s := New(nil, g)
	// Start on A-B: its only usable edges are cut off from the larger component by the steps.
	resp, status, msg := s.plan(cost.Wheelchair, near(g, 0, 0.5), near(g, 3, 0.5))
	if status != http.StatusOK {
		t.Fatalf("status %d %q", status, msg)
	}
	if resp.Accessible || resp.Cost != 0 {
		t.Fatalf("want accessible=false without cost, got %+v", resp)
	}
	if len(resp.Barriers) != 1 || resp.Barriers[0].Description != "Steps" || !slices.Contains(resp.Barriers[0].Reasons, "steps") {
		t.Fatalf("want one steps barrier, got %+v", resp.Barriers)
	}
	if len(resp.Notes) == 0 || resp.Notes[0][:31] != "No fully accessible route for w" {
		t.Errorf("want explanation note first, got %q", resp.Notes)
	}

	// The same trip is fine for walk_winter, which only penalizes steps.
	resp, _, _ = s.plan(cost.WalkWinter, near(g, 0, 0.5), near(g, 3, 0.5))
	if !resp.Accessible || len(resp.Barriers) != 0 {
		t.Errorf("walk_winter: want accessible, got %+v", resp)
	}
}

func TestPlanTooFar(t *testing.T) {
	g := lineGraph()
	s := New(nil, g)
	far := near(g, 0, 0.5)
	for i := range far {
		far[i].distM += maxSnapM
	}
	if _, status, _ := s.plan(cost.Wheelchair, far, near(g, 3, 0.5)); status != http.StatusUnprocessableEntity {
		t.Errorf("status %d, want 422", status)
	}
}
