// Command api serves the Passable HTTP API.
//
//	go run ./cmd/api [-addr :8080] [-db postgres://...] [-cors http://localhost:3000]
//
// Each flag can also come from an env var (PORT, DATABASE_URL, CORS_ORIGINS), which is how
// hosting platforms pass configuration.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/PrateekGithub23/passable/internal/api"
	"github.com/PrateekGithub23/passable/internal/cache"
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/PrateekGithub23/passable/internal/overlay"
	"github.com/PrateekGithub23/passable/internal/reports"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	addr := flag.String("addr", ":"+envOr("PORT", "8080"), "listen address")
	dbURL := flag.String("db", envOr("DATABASE_URL", "postgres://passable:passable@localhost:5433/passable"), "Postgres connection URL")
	cors := flag.String("cors", envOr("CORS_ORIGINS", "http://localhost:3000"), "comma-separated origins allowed to call the API from a browser")
	redisURL := flag.String("redis", envOr("REDIS_URL", "redis://localhost:6379/0"), "Redis URL for the route cache (empty disables it)")
	expiryEvery := flag.Duration("expiry-interval", 30*time.Second, "how often to expire hazard reports past their TTL")
	flag.Parse()

	// ctx is cancelled on Ctrl-C or SIGTERM (what Docker/Fly send on shutdown).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A pool (not a single connection) because many requests query the DB concurrently.
	pool, err := pgxpool.New(ctx, *dbURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	// Load the graph once and precompute per-profile costs; requests then only read them.
	start := time.Now()
	g, err := graph.Load(ctx, pool)
	if err != nil {
		log.Fatalf("load graph: %v", err)
	}
	log.Printf("graph loaded: %d nodes, %d edges in %s", len(g.Nodes), len(g.Edges), time.Since(start).Round(time.Millisecond))

	// Hazards: start with an empty overlay, expire anything overdue, then load every live
	// report from Postgres (the source of truth) into the overlay.
	holder := overlay.NewHolder(overlay.Build(0, nil))
	svc := reports.New(pool, g, holder)
	if _, err := svc.ExpireDue(ctx); err != nil {
		log.Fatalf("expire hazards: %v", err)
	}
	if err := svc.Resync(ctx); err != nil {
		log.Fatalf("load hazards: %v", err)
	}
	log.Printf("hazard overlay: version %d, %d live hazards", holder.Load().Seq, holder.Load().Len())
	go svc.RunExpiry(ctx, *expiryEvery) // background worker; stops when ctx is cancelled

	// Route cache in Redis. Optional: if Redis is unreachable, run without a cache.
	var routeCache *cache.RouteCache
	if *redisURL != "" {
		opts, err := redis.ParseURL(*redisURL)
		if err != nil {
			log.Fatalf("redis url: %v", err)
		}
		rdb := redis.NewClient(opts)
		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Printf("redis unavailable (%v); running without route cache", err)
		} else {
			routeCache = cache.New(rdb, 10*time.Minute)
			log.Printf("route cache: redis %s", opts.Addr)
		}
	}

	srv := api.New(api.Config{DB: pool, Graph: g, Overlay: holder, Reports: svc, Cache: routeCache})

	// Timeouts stop slow or stuck clients from holding connections open forever.
	hs := &http.Server{Addr: *addr, Handler: api.WithCORS(srv, strings.Split(*cors, ",")), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second}
	// Graceful shutdown: on signal, stop accepting new connections and give in-flight
	// requests up to 5 s to finish.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(shutdownCtx)
	}()
	log.Printf("listening on %s", *addr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
