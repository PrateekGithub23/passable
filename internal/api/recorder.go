package api

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Recorder writes one row per route request to route_requests, for benchmarks and the status
// page (latency percentiles, nodes expanded, cache hit rate from real traffic).
//
// It must never slow down routing, so requests only drop a struct into a buffered channel; a
// background goroutine batches rows into one INSERT per second. If the buffer is full (the
// DB is slow or down), stats are dropped and counted, never requests.
type Recorder struct {
	db      *pgxpool.Pool
	ch      chan routeStat
	dropped atomic.Int64
}

type routeStat struct {
	profile    string
	distanceM  *float64 // nil for cache hits (we don't decode the cached body)
	durationMs float64  // whole handler: snapping + search + response building
	expanded   *int32
	version    int64
	cacheHit   bool
	at         time.Time
}

// NewRecorder creates a recorder; call Run in a goroutine to start writing.
func NewRecorder(db *pgxpool.Pool) *Recorder {
	return &Recorder{db: db, ch: make(chan routeStat, 4096)}
}

// record queues a stat without blocking. A nil Recorder ignores it.
func (r *Recorder) record(s routeStat) {
	if r == nil {
		return
	}
	select {
	case r.ch <- s:
	default:
		r.dropped.Add(1)
	}
}

// Run flushes batches every second (or at 500 rows) until ctx is cancelled, then flushes
// what is left.
func (r *Recorder) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var batch []routeStat
	for {
		select {
		case s := <-r.ch:
			batch = append(batch, s)
			if len(batch) < 500 {
				continue
			}
		case <-tick.C:
		case <-ctx.Done():
			r.flush(context.Background(), batch)
			return
		}
		r.flush(ctx, batch)
		batch = batch[:0]
	}
}

// flush inserts a batch with one statement (one array per column, unnest -> rows; the same
// trick as the importer).
func (r *Recorder) flush(ctx context.Context, batch []routeStat) {
	if len(batch) == 0 {
		return
	}
	n := len(batch)
	profiles, dists, durs := make([]string, n), make([]*float64, n), make([]float64, n)
	exps, vers, hits, ats := make([]*int32, n), make([]int64, n), make([]bool, n), make([]time.Time, n)
	for i, s := range batch {
		profiles[i], dists[i], durs[i], exps[i], vers[i], hits[i], ats[i] = s.profile, s.distanceM, s.durationMs, s.expanded, s.version, s.cacheHit, s.at
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := r.db.Exec(ctx, `
		INSERT INTO route_requests (profile, distance_m, duration_ms, nodes_expanded, graph_version, cache_hit, created_at)
		SELECT * FROM unnest($1::text[], $2::float8[], $3::float8[], $4::int[], $5::bigint[], $6::bool[], $7::timestamptz[])`,
		profiles, dists, durs, exps, vers, hits, ats); err != nil {
		log.Printf("recorder: dropped %d stats: %v", n, err)
	}
}
