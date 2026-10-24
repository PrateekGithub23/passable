package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Querier is satisfied by *pgx.Conn and *pgxpool.Pool.
// Accepting this small interface (instead of a concrete type) lets the CLI tools pass a single
// connection and the API server pass its connection pool.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Load reads the full nodes/edges tables into memory.
// It runs once at startup (~0.1-0.2 s); after that, routing never touches the database.
func Load(ctx context.Context, db Querier) (*Graph, error) {
	// Step 1: nodes. ORDER BY makes node indices deterministic across restarts.
	// ST_Y/ST_X extract latitude/longitude from the PostGIS point.
	rows, err := db.Query(ctx, `SELECT id, ST_Y(geom), ST_X(geom) FROM nodes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	var nodes []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.OSMID, &n.Lat, &n.Lon); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	// rows.Err reports errors that ended the loop early (e.g. a dropped connection).
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// OSM node id -> index, to translate edges' from_node/to_node below.
	idx := make(map[int64]int32, len(nodes))
	for i, n := range nodes {
		idx[n.OSMID] = int32(i)
	}

	// Step 2: edges, with the tags the cost model needs. coalesce(...,'') turns SQL NULL
	// (tag absent) into "", which is Go's natural "not set" for strings.
	// The four sidewalk variants are read from the raw JSONB tags and merged in Go.
	// ST_AsGeoJSON returns the line as {"type":"LineString","coordinates":[[lon,lat],...]}.
	rows, err = db.Query(ctx, `
		SELECT id, from_node, to_node, length_m, highway,
		       coalesce(footway,''), coalesce(surface,''), coalesce(smoothness,''), coalesce(incline,''),
		       coalesce(wheelchair,''), coalesce(kerb_start,''), coalesce(kerb_end,''),
		       coalesce(width_m, 0), has_steps, is_crossing,
		       coalesce(tags->>'sidewalk',''), coalesce(tags->>'sidewalk:both',''),
		       coalesce(tags->>'sidewalk:left',''), coalesce(tags->>'sidewalk:right',''),
		       coalesce(tags->>'name',''), ST_AsGeoJSON(geom)
		FROM edges ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	var edges []Edge
	for rows.Next() {
		var e Edge
		var from, to int64
		var sw, swBoth, swLeft, swRight, geojson string
		if err := rows.Scan(&e.ID, &from, &to, &e.LengthM, &e.Highway,
			&e.Footway, &e.Surface, &e.Smoothness, &e.Incline,
			&e.Wheelchair, &e.KerbStart, &e.KerbEnd,
			&e.WidthM, &e.HasSteps, &e.IsCrossing,
			&sw, &swBoth, &swLeft, &swRight, &e.Name, &geojson); err != nil {
			return nil, err
		}
		// Decode only the coordinates array of the GeoJSON line; [2]float64 = [lon, lat].
		var line struct{ Coordinates [][2]float64 }
		if err := json.Unmarshal([]byte(geojson), &line); err != nil || len(line.Coordinates) < 2 {
			return nil, fmt.Errorf("edge %d: bad geometry: %v", e.ID, err)
		}
		e.Geom = make([]Point, len(line.Coordinates))
		for i, c := range line.Coordinates {
			e.Geom[i] = Point{Lat: c[1], Lon: c[0]} // GeoJSON order is lon,lat
		}
		// Translate OSM node ids to dense indices. A miss would mean the DB is inconsistent
		// (the foreign keys should prevent it), so fail loudly instead of routing on bad data.
		var ok1, ok2 bool
		e.From, ok1 = idx[from]
		e.To, ok2 = idx[to]
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("edge %d references unknown node", e.ID)
		}
		e.Sidewalk = normalizeSidewalk(sw, swBoth, swLeft, swRight)
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Step 3: build the adjacency index.
	return New(nodes, edges), nil
}

// normalizeSidewalk collapses OSM's sidewalk tag variants into "yes" (sidewalk is part of
// this way), "separate" (mapped as its own way), "no", or "" (unknown).
//
// OSM spells the same fact several ways (sidewalk=both, sidewalk:left=yes, ...). The checks
// run in priority order: any sign of a sidewalk on this way wins, then "separate", then "no".
func normalizeSidewalk(sw, both, left, right string) string {
	vals := []string{sw, both, left, right}
	for _, v := range vals {
		if v == "both" || v == "left" || v == "right" || v == "yes" {
			return "yes"
		}
	}
	for _, v := range vals {
		if v == "separate" {
			return "separate"
		}
	}
	for _, v := range vals {
		if v == "no" || v == "none" {
			return "no"
		}
	}
	return ""
}
