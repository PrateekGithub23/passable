// Package migrations embeds the SQL migration files into the Go binary, so deploying the API
// or the migrate tool never needs the .sql files on disk next to it.
package migrations

import "embed"

// FS holds every *.sql file in this directory. goose reads them in version order (001, 002, ...).
//
//go:embed *.sql
var FS embed.FS
