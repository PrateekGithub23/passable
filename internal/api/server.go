// Package api serves the HTTP API.
//
// Files: server.go (setup, shared helpers), route.go (GET /api/v1/route), places.go
// (GET /api/v1/places), hazards.go (sessions, hazard reports, graph version), cors.go
// (cross-origin middleware).
package api

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"slices"

	"github.com/PrateekGithub23/passable/internal/cache"
	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/PrateekGithub23/passable/internal/overlay"
	"github.com/PrateekGithub23/passable/internal/reports"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is everything the server needs. Reports and Cache may be nil (tests, or running
// without Redis); DB may be nil only in tests that call plan() directly.
type Config struct {
	DB      *pgxpool.Pool
	Graph   *graph.Graph
	Overlay *overlay.Holder
	Reports *reports.Service
	Cache   *cache.RouteCache
}

// Server holds the graph and per-profile base costs (read-only after New) and the live hazard
// overlay (swapped atomically by the reports service). No locks on the request path.
type Server struct {
	db       *pgxpool.Pool
	g        *graph.Graph
	overlay  *overlay.Holder
	reports  *reports.Service
	cache    *cache.RouteCache
	profiles map[cost.Profile]*profileState
	anyComp  []int32 // components of the full graph, for snapping explanation routes
	anyMain  int32
	mux      *http.ServeMux
}

// profileState is everything precomputed for one profile from the static OSM tags.
type profileState struct {
	table []float64 // base cost per edge; +Inf when the tags block it
	comp  []int32   // components over edges usable by tags alone
	main  int32     // largest of them
}

// blockedCost makes any barrier outweigh a long detour, so the explanation route crosses
// as few barriers as possible. It stays >= length, keeping A* admissible.
func blockedCost(lengthM float64) float64 { return 10_000 + 10*lengthM }

// New precomputes cost tables and components for every profile and registers the routes.
func New(cfg Config) *Server {
	s := &Server{
		db: cfg.DB, g: cfg.Graph, overlay: cfg.Overlay, reports: cfg.Reports, cache: cfg.Cache,
		profiles: map[cost.Profile]*profileState{}, mux: http.NewServeMux(),
	}

	// Components of the whole graph (ignoring profiles). Used when no accessible route exists
	// and we fall back to "the route with the fewest barriers".
	var sizes []int
	s.anyComp, sizes = s.g.Components(nil)
	s.anyMain = largest(sizes)

	for _, p := range cost.Profiles {
		// 1) Base cost of every edge for this profile (~3 ms each).
		ps := &profileState{table: cost.Table(s.g, p)}
		// 2) Components over the edges this profile can use; snapping only picks edges in the
		//    largest one, so a start point is never stranded in an isolated pocket. These are
		//    computed from tags only; a hazard that cuts the network off is caught by the
		//    search itself (no path -> barrier explanation).
		ps.comp, sizes = s.g.Components(func(e int32) bool { return !math.IsInf(ps.table[e], 1) })
		ps.main = largest(sizes)
		s.profiles[p] = ps
	}

	// Go 1.22+ ServeMux patterns include the method and support {id} wildcards.
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/route", s.handleRoute)
	s.mux.HandleFunc("GET /api/v1/places", s.handlePlaces)
	s.mux.HandleFunc("GET /api/v1/graph/version", s.handleVersion)
	s.mux.HandleFunc("POST /api/v1/session", s.handleNewSession)
	s.mux.HandleFunc("GET /api/v1/hazards", s.handleListHazards)
	s.mux.HandleFunc("POST /api/v1/hazards", s.handleCreateHazard)
	s.mux.HandleFunc("POST /api/v1/hazards/{id}/confirm", s.handleVote(reports.Confirm))
	s.mux.HandleFunc("POST /api/v1/hazards/{id}/resolve", s.handleVote(reports.Resolve))
	return s
}

// ServeHTTP makes *Server an http.Handler, so it can be wrapped (e.g. by WithCORS) and served.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// handleHealth is for load balancers and uptime checks: 200 only if the DB answers.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "nodes": len(s.g.Nodes), "edges": len(s.g.Edges)})
}

// view is one query's consistent picture of edge costs: the base table for a profile plus
// ONE overlay snapshot, loaded once. Everything a single request computes goes through it.
type view struct {
	g    *graph.Graph
	p    cost.Profile
	ps   *profileState
	snap *overlay.Snapshot
}

func (s *Server) view(p cost.Profile) view {
	return view{g: s.g, p: p, ps: s.profiles[p], snap: s.overlay.Load()}
}

// cost is the effective cost of edge e: tags first, then live hazards on top.
func (v view) cost(e int32) float64 { return v.snap.Cost(v.p, e, v.ps.table[e]) }

// relaxed is cost with blocks replaced by a huge finite cost (for the barrier explanation).
func (v view) relaxed(e int32) float64 {
	if c := v.cost(e); !math.IsInf(c, 1) {
		return c
	}
	return blockedCost(v.g.Edges[e].LengthM)
}

// usable reports whether a route may start or end on edge e: passable right now (tags and
// hazards), and part of the profile's main network (not an isolated pocket).
func (v view) usable(e int32) bool {
	return !math.IsInf(v.cost(e), 1) && v.ps.comp[v.g.Edges[e].From] == v.ps.main
}

// largest returns the id of the biggest component.
func largest(sizes []int) int32 { return int32(slices.Index(sizes, slices.Max(sizes))) }

// writeJSON sends v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	// Headers must be set before WriteHeader; after it, they are already sent.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent, so all we can do is log (usually a closed client).
		log.Printf("write response: %v", err)
	}
}

// writeError sends {"error": msg}; the frontend shows msg to the user.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
