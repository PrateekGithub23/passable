package graph

import (
	"math"

	"github.com/PrateekGithub23/passable/internal/geo"
)

// Point is a WGS84 coordinate.
type Point struct {
	Lat, Lon float64
}

// Project finds the point on e's geometry closest to (lat, lon). frac is that point's
// position along the edge by length, measured from From (0..1); distM is the distance to it.
//
// Used for snapping a clicked point onto a path: the route then starts frac of the way along
// the edge instead of at one of its ends.
func (e *Edge) Project(lat, lon float64) (frac, distM float64, at Point) {
	// Local equirectangular projection around the query point; exact enough at < 1 km.
	// It turns degrees into metres on a flat x/y plane with the query point at (0, 0):
	// a degree of latitude is always ~111 km, a degree of longitude shrinks by cos(latitude).
	const mPerDegLat = 111_194.93
	mPerDegLon := mPerDegLat * math.Cos(lat*math.Pi/180)
	distM = math.Inf(1)
	along, total := 0.0, 0.0 // metres along the edge to the best point so far / to the current vertex
	// Check every straight piece a->b of the polyline and keep the closest.
	for i := 1; i < len(e.Geom); i++ {
		a, b := e.Geom[i-1], e.Geom[i]
		// a relative to the query point, and the a->b direction vector, both in metres.
		ax, ay := (a.Lon-lon)*mPerDegLon, (a.Lat-lat)*mPerDegLat
		dx, dy := (b.Lon-a.Lon)*mPerDegLon, (b.Lat-a.Lat)*mPerDegLat
		// t = how far along a->b (0..1) the perpendicular from the query point lands.
		// It is the classic vector projection, clamped so the point stays on the segment.
		t := 0.0
		if l2 := dx*dx + dy*dy; l2 > 0 { // l2 == 0: a and b coincide, keep t = 0
			t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l2))
		}
		segLen := geo.Haversine(a.Lat, a.Lon, b.Lat, b.Lon)
		// Distance from the query point (the origin) to the projected point a + t·(b-a).
		if d := math.Hypot(ax+t*dx, ay+t*dy); d < distM {
			distM = d
			along = total + t*segLen
			at = Point{a.Lat + t*(b.Lat-a.Lat), a.Lon + t*(b.Lon-a.Lon)}
		}
		total += segLen
	}
	// Fraction is measured with the same haversine lengths as Edge.LengthM, so
	// frac*LengthM is a consistent "metres from From".
	if total > 0 {
		frac = along / total
	}
	return frac, distM, at
}

// Slice returns the part of e's geometry between fractions f0 and f1, ordered from f0 to f1.
//
// Used to draw partial edges: the first and last edges of a route usually start/end mid-edge,
// and f0 > f1 means the route travels the edge backwards (To -> From).
func (e *Edge) Slice(f0, f1 float64) []Point {
	lo, hi := math.Min(f0, f1), math.Max(f0, f1)
	// cum[i] = metres from the first vertex to vertex i.
	cum := make([]float64, len(e.Geom))
	for i := 1; i < len(e.Geom); i++ {
		a, b := e.Geom[i-1], e.Geom[i]
		cum[i] = cum[i-1] + geo.Haversine(a.Lat, a.Lon, b.Lat, b.Lon)
	}
	total := cum[len(cum)-1]
	// at(d) returns the point d metres along the polyline, interpolating inside a segment.
	at := func(d float64) Point {
		for i := 1; i < len(cum); i++ {
			if d <= cum[i] || i == len(cum)-1 {
				seg := cum[i] - cum[i-1]
				t := 0.0
				if seg > 0 {
					t = math.Max(0, math.Min(1, (d-cum[i-1])/seg))
				}
				a, b := e.Geom[i-1], e.Geom[i]
				return Point{a.Lat + t*(b.Lat-a.Lat), a.Lon + t*(b.Lon-a.Lon)}
			}
		}
		return e.Geom[0]
	}
	// Interpolated start point, every original vertex strictly inside the range (so bends
	// are kept), then the interpolated end point.
	pts := []Point{at(lo * total)}
	for i := 1; i < len(cum)-1; i++ {
		if cum[i] > lo*total && cum[i] < hi*total {
			pts = append(pts, e.Geom[i])
		}
	}
	pts = append(pts, at(hi*total))
	// Built low -> high; reverse if the caller travels the edge backwards.
	if f0 > f1 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	return pts
}
