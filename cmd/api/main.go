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
	"github.com/PrateekGithub23/passable/internal/live"
	"github.com/PrateekGithub23/passable/internal/overlay"
	"github.com/PrateekGithub23/passable/internal/ratelimit"
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
	rateLimit := flag.Bool("rate-limit", envOr("RATE_LIMIT", "true") == "true", "enforce rate limits (needs Redis); disable for load tests")
	ipHeader := flag.String("ip-header", os.Getenv("TRUSTED_IP_HEADER"), "header a trusted proxy sets to the client IP (e.g. Fly-Client-IP); empty = TCP peer")
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

	origins := strings.Split(*cors, ",")
	hub := live.NewHub(origins, func() int64 { return holder.Load().Seq })

	// Redis: route cache + cross-instance event bus. Optional: without Redis, this instance
	// runs alone (no cache, and events only reach its own WebSocket clients).
	var routeCache *cache.RouteCache
	var bus *live.Bus
	var limiter *ratelimit.Limiter
	if *redisURL != "" {
		opts, err := redis.ParseURL(*redisURL)
		if err != nil {
			log.Fatalf("redis url: %v", err)
		}
		// Without this, go-redis ignores context deadlines and falls back to its own
		// multi-second timeouts and retries; the cache's 50 ms and the publish's 500 ms
		// budgets would have no effect.
		opts.ContextTimeoutEnabled = true
		rdb := redis.NewClient(opts)
		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Printf("redis unavailable (%v); running without route cache or event bus", err)
		} else if bus, err = live.NewBus(ctx, rdb); err != nil {
			log.Printf("redis subscribe failed (%v); running without event bus", err)
			routeCache = cache.New(rdb, 10*time.Minute)
		} else {
			routeCache = cache.New(rdb, 10*time.Minute)
			log.Printf("route cache + event bus: redis %s", opts.Addr)
		}
		if routeCache != nil && *rateLimit {
			limiter = ratelimit.New(rdb)
			log.Printf("rate limits: on")
		}
	}

	// How a committed hazard change travels:
	//   with Redis:    writer commits -> publish -> every instance's bus.Run -> apply + push to its clients
	//   without Redis: writer commits -> push to this instance's clients (already applied locally)
	if bus != nil {
		svc.OnCommit = func(ch reports.Change) {
			// This runs inside the hazard POST, after the commit. Bound it tightly: if Redis is
			// down, the writer shouldn't wait on retries. Peers recover from Postgres anyway
			// (gap detection / periodic catch-up), so a lost publish only delays them.
			pctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()
			if err := bus.Publish(pctx, ch); err != nil {
				log.Printf("publish failed (%v); notifying local clients only", err)
				hub.Broadcast(ch)
			}
		}
		go bus.Run(ctx, func(ch reports.Change) {
			svc.ApplyChange(ctx, ch) // no-op for changes this instance wrote itself
			hub.Broadcast(ch)
		})
	} else {
		svc.OnCommit = hub.Broadcast
	}

	// Start the expiry worker only now: it reads svc.OnCommit, which must be set first.
	go svc.RunExpiry(ctx, *expiryEvery) // background worker; stops when ctx is cancelled

	srv := api.New(api.Config{DB: pool, Graph: g, Overlay: holder, Reports: svc, Cache: routeCache, Live: hub, Limiter: limiter, IPHeader: *ipHeader})

	// Timeouts stop slow or stuck clients from holding connections open forever.
	hs := &http.Server{Addr: *addr, Handler: api.WithCORS(srv, origins), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second}
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
