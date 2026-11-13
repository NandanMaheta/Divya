// Package migrations embeds the SQL migration files so the binary is self-contained.
package migrations

import "embed"

// FS holds every *.sql migration, applied in filename order by goose.
//
//go:embed *.sql
var FS embed.FS
