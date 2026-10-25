// Package metrics defines the Prometheus metrics the API exposes at GET /metrics, plus a small
// rolling window of recent propagation samples for the public status page.
//
// Prometheus model in brief: counters only go up (requests served), gauges go up and down
// (connected clients), histograms count observations into buckets (latency), from which
// Prometheus/Grafana compute percentiles over any time range.
package metrics

import (
	"net/http"
	"slices"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// A dedicated registry (instead of the global default) keeps /metrics to exactly what we
// define plus the standard Go runtime and process metrics (heap size, goroutines, CPU).
var registry = prometheus.NewRegistry()

var (
	// RouteDuration is the whole /route handler, labelled by profile and cache hit/miss.
	RouteDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "passable_route_duration_seconds",
		Help:    "Route request handling time (cache lookup, snapping, A*, response).",
		Buckets: []float64{.0005, .001, .002, .004, .008, .016, .032, .064, .128, .256, .512},
	}, []string{"profile", "cache"})

	// NodesExpanded is A* search effort per computed (non-cached) route.
	NodesExpanded = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "passable_route_nodes_expanded",
		Help:    "Nodes settled by A* per computed route.",
		Buckets: prometheus.ExponentialBuckets(16, 2, 12), // 16 .. 32768
	})

	// HazardEvents counts hazard changes applied on this instance, by kind.
	HazardEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "passable_hazard_events_total",
		Help: "Hazard changes received and delivered to WebSocket clients, by kind.",
	}, []string{"kind"})

	// Propagation is the time from a hazard change being committed (on any instance) to it
	// being pushed to this instance's WebSocket clients.
	Propagation = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "passable_hazard_propagation_seconds",
		Help:    "Commit -> WebSocket broadcast latency for hazard changes.",
		Buckets: []float64{.001, .002, .005, .01, .02, .05, .1, .25, .5, 1, 2.5},
	})

	// RateLimited counts requests refused with 429, by rule.
	RateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "passable_rate_limited_total",
		Help: "Requests refused by rate limiting, by rule.",
	}, []string{"rule"})
)

func init() {
	registry.MustRegister(RouteDuration, NodesExpanded, HazardEvents, Propagation, RateLimited,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

// Gauge registers a gauge whose value is read from fn at scrape time (e.g. live hazards,
// connected clients), so nothing has to remember to update it.
func Gauge(name, help string, fn func() float64) {
	registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, fn))
}

// Handler serves the Prometheus text format.
func Handler() http.Handler { return promhttp.HandlerFor(registry, promhttp.HandlerOpts{}) }

// Window keeps the last N samples (e.g. propagation latencies in ms) for quick percentiles on
// the status page. Histograms are better for Prometheus; this is for a human-readable number.
type Window struct {
	mu   sync.Mutex
	buf  []float64
	next int
	full bool
}

// NewWindow keeps the most recent size samples.
func NewWindow(size int) *Window { return &Window{buf: make([]float64, size)} }

// Add records a sample, overwriting the oldest when full (a ring buffer).
func (w *Window) Add(v float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf[w.next] = v
	w.next = (w.next + 1) % len(w.buf)
	w.full = w.full || w.next == 0
}

// Percentiles returns the sample count and the requested percentiles (0-100) of the window.
func (w *Window) Percentiles(ps ...float64) (int, []float64) {
	w.mu.Lock()
	n := w.next
	if w.full {
		n = len(w.buf)
	}
	s := slices.Clone(w.buf[:n])
	w.mu.Unlock()
	out := make([]float64, len(ps))
	if n == 0 {
		return 0, out
	}
	slices.Sort(s)
	for i, p := range ps {
		out[i] = s[int(float64(n-1)*p/100)]
	}
	return n, out
}
