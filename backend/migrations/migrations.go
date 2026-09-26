// Package migrations embeds versioned SQL, not application seed data.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
