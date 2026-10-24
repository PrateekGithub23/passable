package api

// GET /api/v1/route?from=lat,lng&to=lat,lng&profile=wheelchair
//
// Request flow:
//  1. parse and validate the query (handleRoute)
//  2. find candidate edges near each point with a PostGIS KNN query (candidates)
//  3. choose snap points the profile can use and run A* between them (plan)
//  4. if that fails, run A* on the "relaxed" costs to find the fewest-barrier route (plan)
//  5. turn the path into merged, labelled segments and plain-language notes (buildResponse)

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/PrateekGithub23/passable/internal/route"
	"github.com/jackc/pgx/v5"
)

const (
	knnCandidates = 20  // edges fetched by the GiST KNN query, then re-ranked in meters
	maxSnapM      = 150 // farther than this from a usable edge counts as "no usable path nearby"
)

// Service area (south, west, north, east).
const bboxS, bboxW, bboxN, bboxE = 43.455, -80.565, 43.485, -80.505

// routeResponse is the JSON body of a successful /route call (mirrored in web/src/lib/api.ts).
type routeResponse struct {
	Profile       cost.Profile                `json:"profile"`
	Accessible    bool                        `json:"accessible"`
	DistanceM     float64                     `json:"distance_m"`
	Cost          float64                     `json:"cost,omitempty"` // omitted when not accessible
	NodesExpanded int                         `json:"nodes_expanded"`
	SearchMs      float64                     `json:"search_ms"`
	Snap          snapPair                    `json:"snap"`
	ConfidenceM   map[cost.Confidence]float64 `json:"confidence_m"`
	Notes         []string                    `json:"notes"`
	Barriers      []barrier                   `json:"barriers,omitempty"`
	Segments      []segment                   `json:"segments"`
}

type snapPair struct {
	From snapInfo `json:"from"`
	To   snapInfo `json:"to"`
}

// snapInfo says where a clicked point was attached to the network and how far away that was.
type snapInfo struct {
	EdgeID    int64      `json:"edge_id"`
	DistanceM float64    `json:"distance_m"`
	Point     [2]float64 `json:"point"` // [lon, lat]
}

// segment is a run of consecutive edges that look the same to the user (same description,
// name, surface, kerb, confidence and penalties), merged so the list stays short.
type segment struct {
	Description string          `json:"description"`
	Name        string          `json:"name,omitempty"`
	Highway     string          `json:"highway"`
	Surface     string          `json:"surface,omitempty"`
	Kerb        string          `json:"kerb,omitempty"`
	LengthM     float64         `json:"length_m"`
	Confidence  cost.Confidence `json:"confidence"`
	Penalties   []string        `json:"penalties,omitempty"`
	Blocked     bool            `json:"blocked,omitempty"`
	EdgeIDs     []int64         `json:"edge_ids"`
	Coordinates [][2]float64    `json:"coordinates"` // [lon, lat], GeoJSON order
	key         string          // unexported: the merge key, never sent to clients
}

// barrier is a blocked segment on a "no accessible route" response.
type barrier struct {
	Description string     `json:"description"`
	Reasons     []string   `json:"reasons"`
	LengthM     float64    `json:"length_m"`
	Point       [2]float64 `json:"point"` // [lon, lat] of the segment's midpoint
	EdgeIDs     []int64    `json:"edge_ids"`
}

// candidate is one edge near a clicked point, with the exact closest point on it.
type candidate struct {
	edge  int32
	frac  float64 // where along the edge the closest point is (0 = From, 1 = To)
	distM float64 // metres from the clicked point
	at    graph.Point
}

func (c candidate) endpoint() route.Endpoint {
	return route.Endpoint{Edge: c.edge, Frac: c.frac, Lat: c.at.Lat, Lon: c.at.Lon}
}

// handleRoute is the HTTP layer only: validate input, fetch snap candidates, delegate to
// plan, write the result. Keeping the logic in plan (no HTTP, no DB) makes it unit-testable.
func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err := parseLatLng(q.Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "from: "+err.Error())
		return
	}
	to, err := parseLatLng(q.Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "to: "+err.Error())
		return
	}
	profile, err := cost.ParseProfile(q.Get("profile"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// r.Context() is cancelled if the client disconnects, which aborts the DB queries too.
	candFrom, err := s.candidates(r.Context(), from)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snap failed")
		return
	}
	candTo, err := s.candidates(r.Context(), to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snap failed")
		return
	}
	resp, status, msg := s.plan(profile, candFrom, candTo)
	if status != http.StatusOK {
		writeError(w, status, msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// plan picks snap points from the candidates and routes between them. When no fully
// accessible route exists it returns the route with the fewest barriers, naming them.
func (s *Server) plan(profile cost.Profile, candFrom, candTo []candidate) (routeResponse, int, string) {
	ps := s.profiles[profile]

	// Nearest edge on the main network regardless of profile. If even this doesn't exist
	// within maxSnapM, the point is simply too far from any mapped path.
	inAny := func(ei int32) bool { return s.anyComp[s.g.Edges[ei].From] == s.anyMain }
	anyFrom, okFrom := best(candFrom, inAny)
	anyTo, okTo := best(candTo, inAny)
	if !okFrom || !okTo {
		return routeResponse{}, http.StatusUnprocessableEntity,
			fmt.Sprintf("no mapped path within %d m of the %s point", maxSnapM, pick(okFrom, "to", "from"))
	}

	// Nearest edge this profile can actually use (may be farther than anyFrom/anyTo).
	usable := func(ei int32) bool { return ps.usable(s.g, ei) }
	useFrom, uFrom := best(candFrom, usable)
	useTo, uTo := best(candTo, usable)

	// Happy path: both ends snap to usable edges, run A* on the real costs.
	if uFrom && uTo {
		start := time.Now()
		path, err := route.AStarPoints(s.g, func(e int32) float64 { return ps.table[e] }, useFrom.endpoint(), useTo.endpoint())
		elapsed := time.Since(start)
		if err == nil {
			resp := s.buildResponse(profile, path, useFrom, useTo, elapsed, true)
			// Snap notes first (they explain where the route starts), then the route's notes.
			notes := append(s.snapNotes(profile, "start", anyFrom, useFrom), s.snapNotes(profile, "destination", anyTo, useTo)...)
			resp.Notes = append(notes, resp.Notes...)
			if resp.Notes == nil {
				resp.Notes = []string{} // JSON [] rather than null, so clients can always iterate
			}
			return resp, http.StatusOK, ""
		}
		if !errors.Is(err, route.ErrNoPath) {
			return routeResponse{}, http.StatusInternalServerError, "routing failed"
		}
		// ErrNoPath: fall through to the explanation route.
	}

	// Fallback: no fully accessible route. Route between the nearest edges on the relaxed
	// costs (barriers allowed but very expensive); buildResponse marks the barriers it used.
	start := time.Now()
	path, err := route.AStarPoints(s.g, func(e int32) float64 { return ps.relaxed[e] }, anyFrom.endpoint(), anyTo.endpoint())
	elapsed := time.Since(start)
	if err != nil {
		return routeResponse{}, http.StatusInternalServerError, "routing failed"
	}
	return s.buildResponse(profile, path, anyFrom, anyTo, elapsed, false), http.StatusOK, ""
}

// candidates fetches the nearest edges by GiST KNN and re-ranks them by exact distance in meters.
//
// Why two steps: `<->` uses the spatial index, so it is fast, but on a geometry column it
// measures distance in degrees, which ranks slightly differently from metres. Taking 20
// candidates and re-measuring them in Go gets both speed and the right order.
func (s *Server) candidates(ctx context.Context, p graph.Point) ([]candidate, error) {
	// Note the argument order: ST_MakePoint takes (lon, lat).
	rows, err := s.db.Query(ctx,
		`SELECT id FROM edges ORDER BY geom <-> ST_SetSRID(ST_MakePoint($1, $2), 4326) LIMIT $3`,
		p.Lon, p.Lat, knnCandidates)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	cands := make([]candidate, 0, len(ids))
	for _, id := range ids {
		ei, ok := s.g.EdgeByID(id)
		if !ok {
			continue // DB has an edge the in-memory graph doesn't (e.g. reimported since startup)
		}
		frac, d, at := s.g.Edges[ei].Project(p.Lat, p.Lon)
		cands = append(cands, candidate{edge: ei, frac: frac, distM: d, at: at})
	}
	slices.SortFunc(cands, func(a, b candidate) int { return cmpFloat(a.distM, b.distM) })
	return cands, nil
}

// best returns the nearest candidate within maxSnapM that satisfies ok.
// cands is sorted nearest first, so the first match is the nearest one.
func best(cands []candidate, ok func(int32) bool) (candidate, bool) {
	for _, c := range cands {
		if c.distM > maxSnapM {
			break // sorted, so everything after is farther still
		}
		if ok(c.edge) {
			return c, true
		}
	}
	return candidate{}, false
}

// snapNotes explains when the nearest path could not be used and the route starts farther away.
func (s *Server) snapNotes(p cost.Profile, which string, nearest, used candidate) []string {
	// Same edge, or less than 5 m difference: nothing worth mentioning.
	if nearest.edge == used.edge || used.distM-nearest.distM < 5 {
		return nil
	}
	e := &s.g.Edges[nearest.edge]
	// Either the nearest edge itself is blocked (steps, ...) or it is passable but inside an
	// isolated pocket.
	why := "is not connected to the rest of the usable network"
	if r := cost.Evaluate(p, e); r.Blocked {
		why = "has " + strings.Join(r.Reasons, ", ")
	}
	return []string{fmt.Sprintf("The nearest path to the %s (%s, %.0f m away) %s for %s; the route uses a path %.0f m away instead.",
		which, strings.ToLower(describe(e)), nearest.distM, why, p, used.distM)}
}

// buildResponse turns a raw path (edge indices) into what a person can read: merged segments
// with geometry, confidence totals, barriers and notes.
func (s *Server) buildResponse(p cost.Profile, path route.Path, a, b candidate, elapsed time.Duration, accessible bool) routeResponse {
	resp := routeResponse{
		Profile:       p,
		Accessible:    accessible,
		DistanceM:     round1(path.LengthM),
		NodesExpanded: path.Expanded,
		SearchMs:      math.Round(float64(elapsed.Microseconds())) / 1000,
		Snap: snapPair{
			From: snapInfo{EdgeID: s.g.Edges[a.edge].ID, DistanceM: round1(a.distM), Point: [2]float64{a.at.Lon, a.at.Lat}},
			To:   snapInfo{EdgeID: s.g.Edges[b.edge].ID, DistanceM: round1(b.distM), Point: [2]float64{b.at.Lon, b.at.Lat}},
		},
		// All three keys present even when 0, so clients don't need to handle missing keys.
		ConfidenceM: map[cost.Confidence]float64{cost.Verified: 0, cost.Inferred: 0, cost.Unknown: 0},
		Notes:       []string{},
	}
	if accessible {
		resp.Cost = round1(path.Cost) // relaxed costs (with 10,000 m barrier penalties) mean nothing to users
	}

	noKerbCrossings := 0
	// Walk the route piece by piece (partial first/last edges included).
	for _, pc := range pieces(s.g, path, a.endpoint(), b.endpoint()) {
		e := &s.g.Edges[pc.edge]
		ev := cost.Evaluate(p, e) // re-evaluate to get reasons + confidence (the table only stores numbers)
		length := math.Abs(pc.f1-pc.f0) * e.LengthM
		resp.ConfidenceM[ev.Confidence] += length
		kerb := kerbLabel(e)
		if e.IsCrossing && kerb == "" {
			noKerbCrossings++
		}
		// Geometry of just the travelled part, in travel direction, as [lon, lat] pairs.
		coords := make([][2]float64, 0, len(e.Geom))
		for _, pt := range e.Slice(pc.f0, pc.f1) {
			coords = append(coords, [2]float64{pt.Lon, pt.Lat})
		}
		// Merge with the previous segment if everything a user would see is identical.
		key := strings.Join([]string{describe(e), e.Name, e.Surface, kerb, string(ev.Confidence),
			strings.Join(ev.Reasons, ","), strconv.FormatBool(ev.Blocked)}, "|")
		if n := len(resp.Segments); n > 0 && resp.Segments[n-1].key == key {
			last := &resp.Segments[n-1]
			last.LengthM += length
			last.EdgeIDs = append(last.EdgeIDs, e.ID)
			last.Coordinates = append(last.Coordinates, coords[1:]...) // [1:]: skip the shared joint point
			continue
		}
		resp.Segments = append(resp.Segments, segment{
			Description: describe(e), Name: e.Name, Highway: e.Highway, Surface: e.Surface, Kerb: kerb,
			LengthM: length, Confidence: ev.Confidence, Penalties: ev.Reasons, Blocked: ev.Blocked,
			EdgeIDs: []int64{e.ID}, Coordinates: coords, key: key,
		})
	}
	// Second pass: round lengths for display and collect blocked segments as barriers.
	for i := range resp.Segments {
		sg := &resp.Segments[i]
		sg.LengthM = round1(sg.LengthM)
		if sg.Blocked {
			mid := sg.Coordinates[len(sg.Coordinates)/2]
			resp.Barriers = append(resp.Barriers, barrier{
				Description: sg.Description, Reasons: sg.Penalties, LengthM: sg.LengthM, Point: mid, EdgeIDs: sg.EdgeIDs,
			})
		}
	}
	for c, m := range resp.ConfidenceM {
		resp.ConfidenceM[c] = round1(m)
	}

	// Plain-language notes, most important first.
	if !accessible {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"No fully accessible route for %s between these points. The route shown crosses the fewest barriers (%d) this profile cannot use; see barriers.",
			p, len(resp.Barriers)))
	}
	if u := resp.ConfidenceM[cost.Unknown]; u > 0 && path.LengthM > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%.0f m (%.0f%%) of this route has missing surface or kerb data (confidence: unknown).", u, 100*u/path.LengthM))
	}
	if noKerbCrossings > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf("%d crossing segment(s) have no kerb data; kerb ramps there are not confirmed.", noKerbCrossings))
	}
	return resp
}

// piece is one travelled part of an edge.
type piece struct {
	edge   int32
	f0, f1 float64 // fractions along the edge from its From node, in travel order
}

// pieces lists the (partial) edges a point-to-point path travels, in order.
//
// The path's Edges are full edges between nodes; the start and end are partway along
// a.Edge and b.Edge. For each full edge, f0/f1 is 0->1 or 1->0 depending on which end we
// entered from.
func pieces(g *graph.Graph, p route.Path, a, b route.Endpoint) []piece {
	if len(p.Nodes) == 0 {
		// Start and end on the same edge, no node in between.
		return []piece{{a.Edge, a.Frac, b.Frac}}
	}
	at := func(ei, n int32) float64 { // fraction of node n on edge ei
		if g.Edges[ei].From == n {
			return 0
		}
		return 1
	}
	out := []piece{{a.Edge, a.Frac, at(a.Edge, p.Nodes[0])}} // start point -> first node
	for i, ei := range p.Edges {
		f0 := at(ei, p.Nodes[i])
		out = append(out, piece{ei, f0, 1 - f0})
	}
	out = append(out, piece{b.Edge, at(b.Edge, p.Nodes[len(p.Nodes)-1]), b.Frac}) // last node -> end point
	// Drop zero-length pieces (a click exactly on a node gives one).
	return slices.DeleteFunc(out, func(pc piece) bool { return math.Abs(pc.f1-pc.f0) < 1e-9 })
}

// describe gives a short human label for an edge. Order matters: a crossing that is also
// steps is reported as steps, since that's what matters for accessibility.
func describe(e *graph.Edge) string {
	switch {
	case e.HasSteps:
		return "Steps"
	case e.IsCrossing:
		return "Crossing"
	case e.Footway == "sidewalk":
		return "Sidewalk"
	case e.Highway == "pedestrian":
		return "Pedestrian area"
	case e.Highway == "cycleway":
		return "Cycleway"
	case e.Highway == "footway" || e.Highway == "path":
		return "Footpath"
	case e.Highway == "living_street":
		return "Shared street"
	default:
		return "Road (" + e.Highway + ")"
	}
}

// kerbLabel joins the distinct kerb values on an edge, e.g. "lowered" or "lowered, raised".
func kerbLabel(e *graph.Edge) string {
	var ks []string
	for _, k := range []string{e.KerbStart, e.KerbEnd} {
		if k != "" && !slices.Contains(ks, k) {
			ks = append(ks, k)
		}
	}
	return strings.Join(ks, ", ")
}

// parseLatLng parses "lat,lng" and rejects points outside the service area.
func parseLatLng(s string) (graph.Point, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return graph.Point{}, errors.New("want lat,lng")
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lng, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return graph.Point{}, errors.New("want lat,lng")
	}
	if lat < bboxS || lat > bboxN || lng < bboxW || lng > bboxE {
		return graph.Point{}, fmt.Errorf("outside the service area (%g,%g,%g,%g)", bboxS, bboxW, bboxN, bboxE)
	}
	return graph.Point{Lat: lat, Lon: lng}, nil
}

// round1 rounds to 0.1 (decimetres are plenty for walking distances).
func round1(x float64) float64 { return math.Round(x*10) / 10 }

// cmpFloat is a three-way comparison for slices.SortFunc.
func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// pick is a tiny ternary: a if cond, else b.
func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
