// Package seed embeds the data the platform starts with. Each file runs once, in name order (cmd/seed).
package seed

import "embed"

//go:embed *.sql
var FS embed.FS
