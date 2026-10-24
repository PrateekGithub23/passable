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
func (e *Edge) Project(lat, lon float64) (frac, distM float64, at Point) {
	// Local equirectangular projection around the query point; exact enough at < 1 km.
	const mPerDegLat = 111_194.93
	mPerDegLon := mPerDegLat * math.Cos(lat*math.Pi/180)
	distM = math.Inf(1)
	along, total := 0.0, 0.0
	for i := 1; i < len(e.Geom); i++ {
		a, b := e.Geom[i-1], e.Geom[i]
		ax, ay := (a.Lon-lon)*mPerDegLon, (a.Lat-lat)*mPerDegLat
		dx, dy := (b.Lon-a.Lon)*mPerDegLon, (b.Lat-a.Lat)*mPerDegLat
		t := 0.0
		if l2 := dx*dx + dy*dy; l2 > 0 {
			t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l2))
		}
		segLen := geo.Haversine(a.Lat, a.Lon, b.Lat, b.Lon)
		if d := math.Hypot(ax+t*dx, ay+t*dy); d < distM {
			distM = d
			along = total + t*segLen
			at = Point{a.Lat + t*(b.Lat-a.Lat), a.Lon + t*(b.Lon-a.Lon)}
		}
		total += segLen
	}
	if total > 0 {
		frac = along / total
	}
	return frac, distM, at
}

// Slice returns the part of e's geometry between fractions f0 and f1, ordered from f0 to f1.
func (e *Edge) Slice(f0, f1 float64) []Point {
	lo, hi := math.Min(f0, f1), math.Max(f0, f1)
	cum := make([]float64, len(e.Geom))
	for i := 1; i < len(e.Geom); i++ {
		a, b := e.Geom[i-1], e.Geom[i]
		cum[i] = cum[i-1] + geo.Haversine(a.Lat, a.Lon, b.Lat, b.Lon)
	}
	total := cum[len(cum)-1]
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
	pts := []Point{at(lo * total)}
	for i := 1; i < len(cum)-1; i++ {
		if cum[i] > lo*total && cum[i] < hi*total {
			pts = append(pts, e.Geom[i])
		}
	}
	pts = append(pts, at(hi*total))
	if f0 > f1 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	return pts
}
