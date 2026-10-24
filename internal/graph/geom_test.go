package graph

import (
	"math"
	"testing"

	"github.com/PrateekGithub23/passable/internal/geo"
)

// lEdge is an L-shaped edge: 100 m north, then 100 m east.
func lEdge() *Edge {
	const dLat = 100 / 111_194.93
	dLon := dLat / math.Cos(43.47*math.Pi/180)
	e := &Edge{Geom: []Point{{43.47, -80.54}, {43.47 + dLat, -80.54}, {43.47 + dLat, -80.54 + dLon}}}
	for i := 1; i < len(e.Geom); i++ {
		e.LengthM += geo.Haversine(e.Geom[i-1].Lat, e.Geom[i-1].Lon, e.Geom[i].Lat, e.Geom[i].Lon)
	}
	return e
}

func TestProject(t *testing.T) {
	e := lEdge()
	c := e.Geom[1]
	// 10 m west of the midpoint of the first leg.
	frac, d, _ := e.Project((e.Geom[0].Lat+c.Lat)/2, -80.54-10/(111_194.93*math.Cos(43.47*math.Pi/180)))
	if math.Abs(frac-0.25) > 0.002 || math.Abs(d-10) > 0.05 {
		t.Errorf("first leg: frac %.4f dist %.2f, want 0.25 / 10", frac, d)
	}
	// Beyond the far end clamps to frac 1.
	frac, _, at := e.Project(c.Lat, e.Geom[2].Lon+0.001)
	if frac != 1 || at != e.Geom[2] {
		t.Errorf("past end: frac %.4f at %+v", frac, at)
	}
}

func TestSlice(t *testing.T) {
	e := lEdge()
	pts := e.Slice(0.25, 0.75) // crosses the corner vertex
	if len(pts) != 3 || pts[1] != e.Geom[1] {
		t.Fatalf("want start, corner, end; got %+v", pts)
	}
	length := 0.0
	for i := 1; i < len(pts); i++ {
		length += geo.Haversine(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
	}
	if math.Abs(length-0.5*e.LengthM) > 0.1 {
		t.Errorf("slice length %.2f, want %.2f", length, 0.5*e.LengthM)
	}
	rev := e.Slice(0.75, 0.25)
	if rev[0] != pts[2] || rev[2] != pts[0] {
		t.Errorf("reverse slice not reversed: %+v", rev)
	}
	if full := e.Slice(0, 1); len(full) != 3 || full[0] != e.Geom[0] || full[2] != e.Geom[2] {
		t.Errorf("full slice: %+v", full)
	}
}
