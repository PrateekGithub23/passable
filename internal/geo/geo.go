// Package geo holds small geodesic helpers shared by the importer and the router.
package geo

import "math"

// Mean Earth radius (IUGG). Using the same constant everywhere matters: the importer measures
// edge lengths with it and the router's A* heuristic measures straight-line distance with it,
// and the heuristic is only admissible if both use the same yardstick.
const earthRadiusM = 6371008.8

// Haversine returns the great-circle distance in meters between two points.
//
// The haversine formula is numerically stable for the short distances we deal with (metres
// to a few km), where the simpler spherical law of cosines loses precision.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const toRad = math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLon := (lon2 - lon1) * toRad
	// h = sin²(Δlat/2) + cos(lat1)·cos(lat2)·sin²(Δlon/2)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	// Central angle = 2·asin(√h); multiply by the radius to get metres.
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}
