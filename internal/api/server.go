// Package api serves the HTTP API.
//
// Files: server.go (setup, shared helpers), route.go (GET /api/v1/route), places.go
// (GET /api/v1/places), cors.go (cross-origin middleware).
package api

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"slices"

	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Server holds the graph and per-profile cost tables. It is read-only after New.
// Read-only shared state needs no locks: any number of request goroutines can route at once.
type Server struct {
	db       *pgxpool.Pool
	g        *graph.Graph
	profiles map[cost.Profile]*profileState
	anyComp  []int32 // components of the full graph, for snapping explanation routes
	anyMain  int32
	mux      *http.ServeMux
}

// profileState is everything precomputed for one profile.
type profileState struct {
	table   []float64 // cost per edge; +Inf when blocked
	relaxed []float64 // blocked edges made very expensive instead, for explaining barriers
	comp    []int32   // components over usable edges
	main    int32     // largest of them
}

// blockedCost makes any barrier outweigh a long detour, so the explanation route crosses
// as few barriers as possible. It stays >= length, keeping A* admissible.
func blockedCost(lengthM float64) float64 { return 10_000 + 10*lengthM }

// New precomputes cost tables and components for every profile.
func New(db *pgxpool.Pool, g *graph.Graph) *Server {
	s := &Server{db: db, g: g, profiles: map[cost.Profile]*profileState{}, mux: http.NewServeMux()}

	// Components of the whole graph (ignoring profiles). Used when no accessible route exists
	// and we fall back to "the route with the fewest barriers".
	var sizes []int
	s.anyComp, sizes = g.Components(nil)
	s.anyMain = largest(sizes)

	for _, p := range cost.Profiles {
		// 1) Cost of every edge for this profile (~3 ms each).
		ps := &profileState{table: cost.Table(g, p)}
		// 2) The "relaxed" table: same costs, but blocked edges become very expensive instead
		//    of impossible, so a search can still find a route and tell us which barriers it
		//    had to cross.
		ps.relaxed = make([]float64, len(ps.table))
		for i, c := range ps.table {
			if math.IsInf(c, 1) {
				c = blockedCost(g.Edges[i].LengthM)
			}
			ps.relaxed[i] = c
		}
		// 3) Components over the edges this profile can use; snapping only picks edges in the
		//    largest one, so a start point is never stranded in an isolated pocket.
		ps.comp, sizes = g.Components(func(e int32) bool { return !math.IsInf(ps.table[e], 1) })
		ps.main = largest(sizes)
		s.profiles[p] = ps
	}

	// Go 1.22+ ServeMux patterns include the method, so a POST to these paths gets 405
	// automatically.
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/route", s.handleRoute)
	s.mux.HandleFunc("GET /api/v1/places", s.handlePlaces)
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

// usable reports whether profile p can start or end a route on edge ei.
// Both conditions matter: the edge itself must be passable, and it must belong to the main
// network (a passable edge inside a steps-only pocket leads nowhere).
func (ps *profileState) usable(g *graph.Graph, ei int32) bool {
	return !math.IsInf(ps.table[ei], 1) && ps.comp[g.Edges[ei].From] == ps.main
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
