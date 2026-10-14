// Package migrations embeds the SQL migration files applied by internal/store.
//
// Files are named with goose's sequential convention (00001_init.sql). The FS
// is rooted at this directory.
package migrations

import "embed"

// FS holds the embedded migration SQL files.
//
//go:embed *.sql
var FS embed.FS
