// Command api serves the Passable HTTP API.
//
//	go run ./cmd/api [-addr :8080] [-db postgres://...]   (or DATABASE_URL / PORT env vars)
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PrateekGithub23/passable/internal/api"
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	addr := flag.String("addr", ":"+envOr("PORT", "8080"), "listen address")
	dbURL := flag.String("db", envOr("DATABASE_URL", "postgres://passable:passable@localhost:5433/passable"), "Postgres connection URL")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, *dbURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	start := time.Now()
	g, err := graph.Load(ctx, pool)
	if err != nil {
		log.Fatalf("load graph: %v", err)
	}
	srv := api.New(pool, g)
	log.Printf("graph loaded: %d nodes, %d edges in %s", len(g.Nodes), len(g.Edges), time.Since(start).Round(time.Millisecond))

	hs := &http.Server{Addr: *addr, Handler: srv, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second}
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
