// Package geo holds small geodesic helpers shared by the importer and the router.
package geo

import "math"

const earthRadiusM = 6371008.8

// Haversine returns the great-circle distance in meters between two points.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const toRad = math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLon := (lon2 - lon1) * toRad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}
