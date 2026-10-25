package api

import (
	"context"
	"log"
	"math"
	"net/http"
	"time"
)

// GET /api/v1/status: the numbers behind the public /status page. Route statistics come from
// route_requests (real traffic, last 15 minutes, all instances); live counts come from this
// instance's memory.
type statusResponse struct {
	UptimeS     int64             `json:"uptime_s"`
	Graph       statusGraph       `json:"graph"`
	WSClients   int               `json:"ws_clients"`
	Routes      statusRoutes      `json:"routes_15m"`
	Propagation statusPropagation `json:"propagation"`
	GeneratedAt time.Time         `json:"generated_at"`
}

type statusGraph struct {
	Nodes       int   `json:"nodes"`
	Edges       int   `json:"edges"`
	Version     int64 `json:"version"`
	LiveHazards int   `json:"live_hazards"`
}

type statusRoutes struct {
	Requests     int64   `json:"requests"`
	P50Ms        float64 `json:"p50_ms"`
	P95Ms        float64 `json:"p95_ms"`
	P99Ms        float64 `json:"p99_ms"`
	CacheHitRate float64 `json:"cache_hit_rate"` // 0..1
	AvgExpanded  float64 `json:"avg_nodes_expanded"`
}

type statusPropagation struct {
	Samples int     `json:"samples"` // most recent hazard changes seen by this instance
	P50Ms   float64 `json:"p50_ms"`
	P95Ms   float64 `json:"p95_ms"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.overlay.Load()
	resp := statusResponse{
		UptimeS:     int64(time.Since(s.started).Seconds()),
		Graph:       statusGraph{Nodes: len(s.g.Nodes), Edges: len(s.g.Edges), Version: snap.Seq, LiveHazards: snap.Len()},
		GeneratedAt: time.Now().UTC(),
	}
	if s.live != nil {
		resp.WSClients = s.live.Clients()
	}
	if s.propag != nil {
		n, p := s.propag.Percentiles(50, 95)
		resp.Propagation = statusPropagation{Samples: n, P50Ms: round2(p[0]), P95Ms: round2(p[1])}
	}

	// Percentiles computed by Postgres over the last 15 minutes (index on created_at).
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	var rt statusRoutes
	err := s.db.QueryRow(ctx, `
		SELECT count(*),
		       coalesce(percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms), 0),
		       coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms), 0),
		       coalesce(percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms), 0),
		       coalesce(avg(cache_hit::int), 0),
		       coalesce(avg(nodes_expanded) FILTER (WHERE NOT cache_hit), 0)
		FROM route_requests WHERE created_at > now() - interval '15 minutes'`).
		Scan(&rt.Requests, &rt.P50Ms, &rt.P95Ms, &rt.P99Ms, &rt.CacheHitRate, &rt.AvgExpanded)
	if err != nil {
		log.Printf("status: %v", err) // still return the live numbers
	}
	rt.P50Ms, rt.P95Ms, rt.P99Ms = round2(rt.P50Ms), round2(rt.P95Ms), round2(rt.P99Ms)
	rt.CacheHitRate, rt.AvgExpanded = round2(rt.CacheHitRate), math.Round(rt.AvgExpanded)
	resp.Routes = rt
	writeJSON(w, http.StatusOK, resp)
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
