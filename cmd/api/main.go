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
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	addr := flag.String("addr", ":"+envOr("PORT", "8080"), "listen address")
	dbURL := flag.String("db", envOr("DATABASE_URL", "postgres://passable:passable@localhost:5433/passable"), "Postgres connection URL")
	cors := flag.String("cors", envOr("CORS_ORIGINS", "http://localhost:3000"), "comma-separated origins allowed to call the API from a browser")
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
	srv := api.New(pool, g)
	log.Printf("graph loaded: %d nodes, %d edges in %s", len(g.Nodes), len(g.Edges), time.Since(start).Round(time.Millisecond))

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
