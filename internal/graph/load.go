package graph

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Querier is satisfied by *pgx.Conn and *pgxpool.Pool.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Load reads the full nodes/edges tables into memory.
func Load(ctx context.Context, db Querier) (*Graph, error) {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	idx := make(map[int64]int32, len(nodes))
	for i, n := range nodes {
		idx[n.OSMID] = int32(i)
	}

	rows, err = db.Query(ctx, `
		SELECT id, from_node, to_node, length_m, highway,
		       coalesce(footway,''), coalesce(surface,''), coalesce(smoothness,''), coalesce(incline,''),
		       coalesce(wheelchair,''), coalesce(kerb_start,''), coalesce(kerb_end,''),
		       coalesce(width_m, 0), has_steps, is_crossing,
		       coalesce(tags->>'sidewalk',''), coalesce(tags->>'sidewalk:both',''),
		       coalesce(tags->>'sidewalk:left',''), coalesce(tags->>'sidewalk:right','')
		FROM edges ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	var edges []Edge
	for rows.Next() {
		var e Edge
		var from, to int64
		var sw, swBoth, swLeft, swRight string
		if err := rows.Scan(&e.ID, &from, &to, &e.LengthM, &e.Highway,
			&e.Footway, &e.Surface, &e.Smoothness, &e.Incline,
			&e.Wheelchair, &e.KerbStart, &e.KerbEnd,
			&e.WidthM, &e.HasSteps, &e.IsCrossing,
			&sw, &swBoth, &swLeft, &swRight); err != nil {
			return nil, err
		}
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
	return New(nodes, edges), nil
}

// normalizeSidewalk collapses OSM's sidewalk tag variants into "yes" (sidewalk is part of
// this way), "separate" (mapped as its own way), "no", or "" (unknown).
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
