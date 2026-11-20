package postgres

import (
	"context"
	_ "embed"
)

//go:embed demo_live_seed.sql
var demoLiveSeed string

// SeedDemoLive adds the live sessions of the CEBA demo (classes on the platform and on Meet, a tutoring and a
// psychology appointment); it needs the classroom, the tutoring and the psychology demo data already in place
func SeedDemoLive(ctx context.Context, c *Client) error {
	_, err := c.pool.Exec(ctx, demoLiveSeed)
	return err
}
