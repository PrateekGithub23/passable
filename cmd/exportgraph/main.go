// Command exportgraph writes the imported graph to a gzipped fixture used by golden-route
// tests, property tests and benchmarks (see internal/graph/fixture.go). Re-run it after
// re-importing OSM data, then re-check the golden routes.
//
//	go run ./cmd/exportgraph -out testdata/campus-graph.json.gz
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/jackc/pgx/v5"
)

func main() {
	dbURL := flag.String("db", "postgres://passable:passable@localhost:5433/passable", "Postgres connection URL")
	out := flag.String("out", "testdata/campus-graph.json.gz", "output file")
	flag.Parse()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		log.Fatal(err)
	}
	g, err := graph.Load(ctx, conn)
	conn.Close(ctx)
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := graph.WriteFixture(f, g); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	st, _ := os.Stat(*out)
	log.Printf("wrote %s: %d nodes, %d edges, %d KB", *out, len(g.Nodes), len(g.Edges), st.Size()/1024)
}
