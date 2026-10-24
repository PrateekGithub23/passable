// Command importer loads Overpass JSON (ways with geometry + tagged nodes) into the
// nodes/edges tables, splitting ways into graph edges at intersections.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PrateekGithub23/passable/internal/geo"
	"github.com/jackc/pgx/v5"
)

const batchSize = 5000

type latLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type element struct {
	Type     string            `json:"type"`
	ID       int64             `json:"id"`
	Lat      float64           `json:"lat"`
	Lon      float64           `json:"lon"`
	Nodes    []int64           `json:"nodes"`
	Geometry []latLon          `json:"geometry"`
	Tags     map[string]string `json:"tags"`
}

type overpassFile struct {
	Elements []element `json:"elements"`
}

type edge struct {
	wayID      int64
	from, to   int64
	lengthM    float64
	wkt        string
	highway    string
	footway    *string
	surface    *string
	smoothness *string
	incline    *string
	widthM     *float64
	wheelchair *string
	kerbStart  *string
	kerbEnd    *string
	hasSteps   bool
	isCrossing bool
	tagsJSON   string
}

func main() {
	waysPath := flag.String("ways", "data/ways.json", "Overpass JSON with ways (out geom)")
	nodesPath := flag.String("nodes", "data/nodes.json", "Overpass JSON with kerb/crossing nodes")
	dbURL := flag.String("db", "postgres://passable:passable@localhost:5433/passable", "Postgres connection URL")
	flag.Parse()

	start := time.Now()
	ways, err := readElements(*waysPath)
	if err != nil {
		log.Fatalf("read ways: %v", err)
	}
	tagged, err := readElements(*nodesPath)
	if err != nil {
		log.Fatalf("read nodes: %v", err)
	}

	kerbs := make(map[int64]string)
	for _, n := range tagged {
		if k, ok := n.Tags["kerb"]; ok {
			kerbs[n.ID] = k
		}
	}

	edges, coords, skipped := buildEdges(ways, kerbs)
	log.Printf("ways=%d -> edges=%d (skipped %d zero-length/self-loop stretches), graph nodes=%d, kerb nodes=%d",
		len(ways), len(edges), skipped, len(coords), len(kerbs))

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if err := load(ctx, conn, coords, edges); err != nil {
		log.Fatalf("load: %v", err)
	}
	log.Printf("import committed in %s", time.Since(start).Round(time.Millisecond))
}

func readElements(path string) ([]element, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var of overpassFile
	if err := json.NewDecoder(f).Decode(&of); err != nil {
		return nil, err
	}
	return of.Elements, nil
}

// buildEdges splits every way at its endpoints and at nodes shared with another way.
// It returns the edges plus coordinates of the graph nodes (edge endpoints only).
func buildEdges(ways []element, kerbs map[int64]string) ([]edge, map[int64]latLon, int) {
	uses := make(map[int64]int)
	for _, w := range ways {
		seen := make(map[int64]bool, len(w.Nodes))
		for _, id := range w.Nodes {
			if !seen[id] {
				seen[id] = true
				uses[id]++
			}
		}
	}

	var edges []edge
	coords := make(map[int64]latLon)
	skipped := 0
	for _, w := range ways {
		if w.Type != "way" || len(w.Nodes) < 2 || len(w.Geometry) != len(w.Nodes) {
			continue
		}
		start := 0
		for i := 1; i < len(w.Nodes); i++ {
			if i != len(w.Nodes)-1 && uses[w.Nodes[i]] <= 1 {
				continue
			}
			e, ok := makeEdge(w, start, i, kerbs)
			if ok {
				edges = append(edges, e)
				coords[e.from] = w.Geometry[start]
				coords[e.to] = w.Geometry[i]
			} else {
				skipped++
			}
			start = i
		}
	}
	return edges, coords, skipped
}

func makeEdge(w element, i, j int, kerbs map[int64]string) (edge, bool) {
	from, to := w.Nodes[i], w.Nodes[j]
	pts := w.Geometry[i : j+1]
	length := 0.0
	for k := 1; k < len(pts); k++ {
		length += geo.Haversine(pts[k-1].Lat, pts[k-1].Lon, pts[k].Lat, pts[k].Lon)
	}
	if from == to || length == 0 {
		return edge{}, false
	}

	var sb strings.Builder
	sb.WriteString("LINESTRING(")
	for k, p := range pts {
		if k > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, "%.7f %.7f", p.Lon, p.Lat)
	}
	sb.WriteString(")")

	// Kerb nodes often sit mid-way on a crossing rather than at a split point, so take the
	// first and last kerb-tagged nodes anywhere in the stretch.
	var kerbStart, kerbEnd *string
	first, last := -1, -1
	for k := i; k <= j; k++ {
		if _, ok := kerbs[w.Nodes[k]]; ok {
			if first < 0 {
				first = k
			}
			last = k
		}
	}
	if first >= 0 {
		kerbStart = ptr(kerbs[w.Nodes[first]])
		if last != first {
			kerbEnd = ptr(kerbs[w.Nodes[last]])
		}
	}

	tagsJSON, _ := json.Marshal(w.Tags)
	t := w.Tags
	return edge{
		wayID:      w.ID,
		from:       from,
		to:         to,
		lengthM:    length,
		wkt:        sb.String(),
		highway:    t["highway"],
		footway:    tagPtr(t, "footway"),
		surface:    tagPtr(t, "surface"),
		smoothness: tagPtr(t, "smoothness"),
		incline:    tagPtr(t, "incline"),
		widthM:     parseWidth(t["width"]),
		wheelchair: tagPtr(t, "wheelchair"),
		kerbStart:  kerbStart,
		kerbEnd:    kerbEnd,
		hasSteps:   t["highway"] == "steps",
		isCrossing: t["footway"] == "crossing" || t["highway"] == "crossing",
		tagsJSON:   string(tagsJSON),
	}, true
}

func load(ctx context.Context, conn *pgx.Conn, coords map[int64]latLon, edges []edge) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "TRUNCATE edges, nodes RESTART IDENTITY CASCADE"); err != nil {
		return err
	}

	ids := make([]int64, 0, len(coords))
	for id := range coords {
		ids = append(ids, id)
	}
	for b := 0; b < len(ids); b += batchSize {
		chunk := ids[b:min(b+batchSize, len(ids))]
		lons := make([]float64, len(chunk))
		lats := make([]float64, len(chunk))
		for k, id := range chunk {
			lons[k], lats[k] = coords[id].Lon, coords[id].Lat
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO nodes (id, geom)
			SELECT id, ST_SetSRID(ST_MakePoint(lon, lat), 4326)
			FROM unnest($1::bigint[], $2::float8[], $3::float8[]) AS t(id, lon, lat)`,
			chunk, lons, lats); err != nil {
			return fmt.Errorf("insert nodes: %w", err)
		}
	}

	for b := 0; b < len(edges); b += batchSize {
		chunk := edges[b:min(b+batchSize, len(edges))]
		n := len(chunk)
		var (
			wayIDs, froms, tos                                 = make([]int64, n), make([]int64, n), make([]int64, n)
			lengths                                            = make([]float64, n)
			wkts, highways, tags                               = make([]string, n), make([]string, n), make([]string, n)
			footways, surfaces, smooths, inclines, wheelchairs = make([]*string, n), make([]*string, n), make([]*string, n), make([]*string, n), make([]*string, n)
			kerbStarts, kerbEnds                               = make([]*string, n), make([]*string, n)
			widths                                             = make([]*float64, n)
			steps, crossings                                   = make([]bool, n), make([]bool, n)
		)
		for k, e := range chunk {
			wayIDs[k], froms[k], tos[k], lengths[k] = e.wayID, e.from, e.to, e.lengthM
			wkts[k], highways[k], tags[k] = e.wkt, e.highway, e.tagsJSON
			footways[k], surfaces[k], smooths[k], inclines[k], wheelchairs[k] = e.footway, e.surface, e.smoothness, e.incline, e.wheelchair
			kerbStarts[k], kerbEnds[k], widths[k] = e.kerbStart, e.kerbEnd, e.widthM
			steps[k], crossings[k] = e.hasSteps, e.isCrossing
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO edges (osm_way_id, from_node, to_node, length_m, geom, highway, footway, surface,
			                   smoothness, incline, width_m, wheelchair, kerb_start, kerb_end, has_steps, is_crossing, tags)
			SELECT way_id, from_node, to_node, length_m, ST_GeomFromText(wkt, 4326), highway, footway, surface,
			       smoothness, incline, width_m, wheelchair, kerb_start, kerb_end, has_steps, is_crossing, tags::jsonb
			FROM unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::float8[], $5::text[], $6::text[], $7::text[],
			            $8::text[], $9::text[], $10::text[], $11::float8[], $12::text[], $13::text[], $14::text[],
			            $15::bool[], $16::bool[], $17::text[])
			  AS t(way_id, from_node, to_node, length_m, wkt, highway, footway, surface, smoothness, incline,
			       width_m, wheelchair, kerb_start, kerb_end, has_steps, is_crossing, tags)`,
			wayIDs, froms, tos, lengths, wkts, highways, footways, surfaces, smooths, inclines,
			widths, wheelchairs, kerbStarts, kerbEnds, steps, crossings, tags); err != nil {
			return fmt.Errorf("insert edges: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// parseWidth accepts "1.5" or "1.5 m"; anything else (feet, ranges) is treated as unknown.
func parseWidth(s string) *float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "m"))
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return nil
	}
	return &v
}

func tagPtr(t map[string]string, k string) *string {
	if v, ok := t[k]; ok {
		return &v
	}
	return nil
}

func ptr(s string) *string { return &s }
