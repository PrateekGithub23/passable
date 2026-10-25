// Command migrate applies database migrations with goose.
//
//	go run ./cmd/migrate            # apply all pending migrations ("up")
//	go run ./cmd/migrate status     # list migrations and whether each is applied
//	go run ./cmd/migrate down       # roll back the most recent migration
//
// goose records applied versions in the goose_db_version table, so running "up" twice is safe.
package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"

	"github.com/PrateekGithub23/passable/db/migrations"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver for database/sql, which goose uses
	"github.com/pressly/goose/v3"
)

func main() {
	dbURL := flag.String("db", envOr("DATABASE_URL", "postgres://passable:passable@localhost:5433/passable"), "Postgres connection URL")
	flag.Parse()
	command := "up"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}

	db, err := sql.Open("pgx", *dbURL)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Read migrations from the embedded files rather than the filesystem.
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatal(err)
	}
	// "." = the root of the embedded FS, where the .sql files are.
	if err := goose.RunContext(context.Background(), command, db, "."); err != nil {
		log.Fatalf("goose %s: %v", command, err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
