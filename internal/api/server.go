// Package api serves the HTTP API.
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
type Server struct {
	db       *pgxpool.Pool
	g        *graph.Graph
	profiles map[cost.Profile]*profileState
	anyComp  []int32 // components of the full graph, for snapping explanation routes
	anyMain  int32
	mux      *http.ServeMux
}

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
	var sizes []int
	s.anyComp, sizes = g.Components(nil)
	s.anyMain = largest(sizes)
	for _, p := range cost.Profiles {
		ps := &profileState{table: cost.Table(g, p)}
		ps.relaxed = make([]float64, len(ps.table))
		for i, c := range ps.table {
			if math.IsInf(c, 1) {
				c = blockedCost(g.Edges[i].LengthM)
			}
			ps.relaxed[i] = c
		}
		ps.comp, sizes = g.Components(func(e int32) bool { return !math.IsInf(ps.table[e], 1) })
		ps.main = largest(sizes)
		s.profiles[p] = ps
	}

	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/route", s.handleRoute)
	s.mux.HandleFunc("GET /api/v1/places", s.handlePlaces)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "nodes": len(s.g.Nodes), "edges": len(s.g.Edges)})
}

// usable reports whether profile p can start or end a route on edge ei.
func (ps *profileState) usable(g *graph.Graph, ei int32) bool {
	return !math.IsInf(ps.table[ei], 1) && ps.comp[g.Edges[ei].From] == ps.main
}

func largest(sizes []int) int32 { return int32(slices.Index(sizes, slices.Max(sizes))) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
