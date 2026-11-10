// Package migrations embeds the SQL migrations so the binaries carry them.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
