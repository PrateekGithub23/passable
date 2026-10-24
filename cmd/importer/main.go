// Command importer loads Overpass JSON (ways with geometry, tagged nodes, named places) into
// the nodes/edges/places tables, splitting ways into graph edges at intersections.
//
// Pipeline:
//
//	data/ways.json   --(split at intersections)-->  edges + graph nodes
//	data/nodes.json  --(kerb tags by node id)----->  kerb_start / kerb_end on edges
//	data/places.json --(name, ref, center)-------->  places (for search)
//
// Everything is written in ONE transaction, so a failed import leaves the old data intact.
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

// Rows per INSERT statement. Big enough to avoid thousands of round trips, small enough that
// one statement's parameter arrays stay a few MB.
const batchSize = 5000

type latLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// element is one entry of an Overpass JSON response. Which fields are filled depends on the
// element type and the query's "out" mode:
//   - node:                 Lat/Lon
//   - way with "out geom":  Nodes (OSM node ids) and Geometry (their coordinates, same order)
//   - way/relation with "out center": Center
type element struct {
	Type     string            `json:"type"`
	ID       int64             `json:"id"`
	Lat      float64           `json:"lat"`
	Lon      float64           `json:"lon"`
	Nodes    []int64           `json:"nodes"`
	Geometry []latLon          `json:"geometry"`
	Center   *latLon           `json:"center"` // ways/relations with "out center"
	Tags     map[string]string `json:"tags"`
}

// place is one row for the places table. Pointer fields map to nullable columns (nil = NULL).
type place struct {
	osmType  string
	osmID    int64
	name     string
	ref      *string
	aliases  *string
	kind     string
	lat, lon float64
}

type overpassFile struct {
	Elements []element `json:"elements"`
}

// edge is one row for the edges table. Pointer fields map to nullable columns: a missing OSM
// tag becomes NULL, which keeps "not tagged" distinct from any real value.
type edge struct {
	wayID      int64
	from, to   int64 // OSM node ids of the endpoints
	lengthM    float64
	wkt        string // geometry as WKT text, e.g. "LINESTRING(-80.54 43.47, ...)"
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
	tagsJSON   string // every raw OSM tag, stored as JSONB for anything we don't have a column for
}

func main() {
	waysPath := flag.String("ways", "data/ways.json", "Overpass JSON with ways (out geom)")
	nodesPath := flag.String("nodes", "data/nodes.json", "Overpass JSON with kerb/crossing nodes")
	placesPath := flag.String("places", "data/places.json", "Overpass JSON with named places (out center)")
	dbURL := flag.String("db", "postgres://passable:passable@localhost:5433/passable", "Postgres connection URL")
	flag.Parse()

	// Step 1: read the three downloaded files.
	start := time.Now()
	ways, err := readElements(*waysPath)
	if err != nil {
		log.Fatalf("read ways: %v", err)
	}
	tagged, err := readElements(*nodesPath)
	if err != nil {
		log.Fatalf("read nodes: %v", err)
	}

	// Step 2: index kerb tags by node id, so splitting can look them up in O(1).
	kerbs := make(map[int64]string)
	for _, n := range tagged {
		if k, ok := n.Tags["kerb"]; ok {
			kerbs[n.ID] = k
		}
	}

	placeElems, err := readElements(*placesPath)
	if err != nil {
		log.Fatalf("read places: %v", err)
	}

	// Step 3: turn OSM ways into graph edges, and OSM named features into places.
	edges, coords, skipped := buildEdges(ways, kerbs)
	log.Printf("ways=%d -> edges=%d (skipped %d zero-length/self-loop stretches), graph nodes=%d, kerb nodes=%d",
		len(ways), len(edges), skipped, len(coords), len(kerbs))
	places := buildPlaces(placeElems)
	log.Printf("places=%d (from %d elements)", len(places), len(placeElems))

	// Step 4: write everything to Postgres in one transaction.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if err := load(ctx, conn, coords, edges, places); err != nil {
		log.Fatalf("load: %v", err)
	}
	log.Printf("import committed in %s", time.Since(start).Round(time.Millisecond))
}

// readElements decodes an Overpass JSON file and returns its "elements" array.
func readElements(path string) ([]element, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var of overpassFile
	// Streaming decode straight from the file; no need to hold the raw bytes in memory too.
	if err := json.NewDecoder(f).Decode(&of); err != nil {
		return nil, err
	}
	return of.Elements, nil
}

// buildEdges splits every way at its endpoints and at nodes shared with another way.
// It returns the edges plus coordinates of the graph nodes (edge endpoints only).
//
// Why split: an OSM way is a named line (e.g. a whole sidewalk) that other ways join partway
// along. A routing graph needs a vertex at every junction, so each way is cut wherever a node
// is also used by another way.
func buildEdges(ways []element, kerbs map[int64]string) ([]edge, map[int64]latLon, int) {
	// Pass 1: count how many distinct ways use each node. A count > 1 means "junction".
	uses := make(map[int64]int)
	for _, w := range ways {
		// A closed way (a loop) lists its first node twice; count each way once per node.
		seen := make(map[int64]bool, len(w.Nodes))
		for _, id := range w.Nodes {
			if !seen[id] {
				seen[id] = true
				uses[id]++
			}
		}
	}

	// Pass 2: walk each way and emit an edge every time we reach a junction or the end.
	var edges []edge
	coords := make(map[int64]latLon) // graph node id -> location (only edge endpoints)
	skipped := 0
	for _, w := range ways {
		// Defensive: skip anything that isn't a well-formed way with matching geometry.
		if w.Type != "way" || len(w.Nodes) < 2 || len(w.Geometry) != len(w.Nodes) {
			continue
		}
		start := 0 // index in w.Nodes where the current stretch began
		for i := 1; i < len(w.Nodes); i++ {
			// Keep walking through ordinary shape points (used by this way only).
			if i != len(w.Nodes)-1 && uses[w.Nodes[i]] <= 1 {
				continue
			}
			// Node i ends the stretch [start, i]: either a junction or the way's last node.
			e, ok := makeEdge(w, start, i, kerbs)
			if ok {
				edges = append(edges, e)
				coords[e.from] = w.Geometry[start]
				coords[e.to] = w.Geometry[i]
			} else {
				skipped++
			}
			start = i // the next stretch starts where this one ended
		}
	}
	return edges, coords, skipped
}

// makeEdge builds the edge for nodes i..j (inclusive) of way w. ok is false for degenerate
// stretches (a loop back to the same node, or zero length) that would be useless to route on.
func makeEdge(w element, i, j int, kerbs map[int64]string) (edge, bool) {
	from, to := w.Nodes[i], w.Nodes[j]
	pts := w.Geometry[i : j+1]

	// Length = sum of great-circle distances between consecutive shape points, so it follows
	// the path's curves instead of cutting straight between the endpoints.
	length := 0.0
	for k := 1; k < len(pts); k++ {
		length += geo.Haversine(pts[k-1].Lat, pts[k-1].Lon, pts[k].Lat, pts[k].Lon)
	}
	if from == to || length == 0 {
		return edge{}, false
	}

	// Build WKT for PostGIS. Note the order: WKT/PostGIS want "lon lat", not "lat lon".
	// 7 decimal places is ~1 cm, the precision OSM stores.
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
		// Only fill kerb_end when there is a second, different kerb node.
		if last != first {
			kerbEnd = ptr(kerbs[w.Nodes[last]])
		}
	}

	// Copy the tags we model into typed columns; keep all of them as JSON as well.
	tagsJSON, _ := json.Marshal(w.Tags) // a map[string]string always marshals
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

// load replaces the contents of nodes, edges and places in a single transaction.
//
// Batching trick: instead of one INSERT per row (tens of thousands of round trips), each batch
// sends one array per column and Postgres's unnest() turns the arrays back into rows. One
// statement inserts up to batchSize rows.
func load(ctx context.Context, conn *pgx.Conn, coords map[int64]latLon, edges []edge, places []place) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	// If anything below returns early, roll back. After a successful Commit this is a no-op.
	defer tx.Rollback(ctx)

	// Start from empty tables. RESTART IDENTITY resets edges.id back to 1; CASCADE also
	// clears tables that reference these (none yet, but hazard reports will reference edges).
	if _, err := tx.Exec(ctx, "TRUNCATE edges, nodes, places RESTART IDENTITY CASCADE"); err != nil {
		return err
	}

	// Nodes first: edges have foreign keys to nodes, so the nodes must exist.
	ids := make([]int64, 0, len(coords))
	for id := range coords {
		ids = append(ids, id)
	}
	for b := 0; b < len(ids); b += batchSize {
		chunk := ids[b:min(b+batchSize, len(ids))]
		// Column arrays for this batch.
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

	// Edges, same pattern: one array per column. Nil pointers in the []*string arrays become
	// NULL elements, so missing tags stay NULL in the table.
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
		// Transpose: row structs -> column arrays.
		for k, e := range chunk {
			wayIDs[k], froms[k], tos[k], lengths[k] = e.wayID, e.from, e.to, e.lengthM
			wkts[k], highways[k], tags[k] = e.wkt, e.highway, e.tagsJSON
			footways[k], surfaces[k], smooths[k], inclines[k], wheelchairs[k] = e.footway, e.surface, e.smoothness, e.incline, e.wheelchair
			kerbStarts[k], kerbEnds[k], widths[k] = e.kerbStart, e.kerbEnd, e.widthM
			steps[k], crossings[k] = e.hasSteps, e.isCrossing
		}
		// ST_GeomFromText parses the WKT; tags::jsonb parses the JSON text.
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

	// Places: independent of the graph, same batching pattern.
	for b := 0; b < len(places); b += batchSize {
		chunk := places[b:min(b+batchSize, len(places))]
		n := len(chunk)
		types, names, kinds := make([]string, n), make([]string, n), make([]string, n)
		ids := make([]int64, n)
		refs, aliases := make([]*string, n), make([]*string, n)
		lats, lons := make([]float64, n), make([]float64, n)
		for k, p := range chunk {
			types[k], ids[k], names[k], refs[k], aliases[k], kinds[k] = p.osmType, p.osmID, p.name, p.ref, p.aliases, p.kind
			lats[k], lons[k] = p.lat, p.lon
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO places (osm_type, osm_id, name, ref, aliases, kind, geom)
			SELECT t, id, name, ref, aliases, kind, ST_SetSRID(ST_MakePoint(lon, lat), 4326)
			FROM unnest($1::text[], $2::bigint[], $3::text[], $4::text[], $5::text[], $6::text[], $7::float8[], $8::float8[])
			  AS u(t, id, name, ref, aliases, kind, lat, lon)`,
			types, ids, names, refs, aliases, kinds, lats, lons); err != nil {
			return fmt.Errorf("insert places: %w", err)
		}
	}

	// Nothing is visible to other connections until this commit succeeds.
	return tx.Commit(ctx)
}

// buildPlaces keeps named elements that have a location, classifying each by its main tag.
func buildPlaces(elems []element) []place {
	var out []place
	for _, el := range elems {
		name := el.Tags["name"]
		// Nodes carry their own lat/lon; ways and relations get a center from "out center".
		lat, lon := el.Lat, el.Lon
		if el.Center != nil {
			lat, lon = el.Center.Lat, el.Center.Lon
		}
		if name == "" || (lat == 0 && lon == 0) {
			continue
		}
		// kind = the first specific tag value, in priority order. "building=yes" says nothing
		// useful, so it is skipped in favour of e.g. amenity=cafe.
		kind := ""
		for _, k := range []string{"amenity", "shop", "leisure", "tourism", "office", "building"} {
			if v := el.Tags[k]; v != "" && v != "yes" {
				kind = v
				break
			}
		}
		if kind == "" {
			kind = "building"
		}
		// Other names people might search by. old_name/old_ref cover renamed buildings
		// (e.g. "E7" is now the Pearl Sullivan Engineering Building).
		var aliases []string
		for _, k := range []string{"short_name", "alt_name", "official_name", "old_name", "old_ref", "loc_name"} {
			if v := el.Tags[k]; v != "" && v != name {
				aliases = append(aliases, v)
			}
		}
		p := place{osmType: el.Type, osmID: el.ID, name: name, ref: tagPtr(el.Tags, "ref"), kind: kind, lat: lat, lon: lon}
		if len(aliases) > 0 {
			p.aliases = ptr(strings.Join(aliases, " "))
		}
		out = append(out, p)
	}
	return out
}

// parseWidth accepts "1.5" or "1.5 m"; anything else (feet, ranges) is treated as unknown.
func parseWidth(s string) *float64 {
	// "1.5 m" -> "1.5 " -> "1.5"
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

// tagPtr returns a pointer to tag k's value, or nil if the tag is absent (-> SQL NULL).
func tagPtr(t map[string]string, k string) *string {
	if v, ok := t[k]; ok {
		return &v
	}
	return nil
}

// ptr returns a pointer to a copy of s.
func ptr(s string) *string { return &s }
