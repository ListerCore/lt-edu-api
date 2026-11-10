package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"api/migrations/seed"
)

// Any constant shared by every replica: only one of them seeds at a time
const seedLockKey = 7_301_250_101

// Seeder applies migrations/seed: each file once, in name order, inside its own transaction, recorded in seed_run
// (the seeds' own bookkeeping, like schema_migrations for the migrations)
type Seeder struct {
	pool *pgxpool.Pool
}

func NewSeeder(dsn string) (*Seeder, error) {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, err
	}
	return &Seeder{pool: pool}, nil
}

func (s *Seeder) Close() { s.pool.Close() }

// Run applies the pending files and returns their names (empty when everything was already applied)
func (s *Seeder) Run(ctx context.Context) ([]string, error) {
	names, err := fs.Glob(seed.FS, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, seedLockKey); err != nil {
		return nil, err
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, seedLockKey) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS seed_run (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}

	var applied []string
	for _, name := range names {
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM seed_run WHERE name = $1)`, name).Scan(&done); err != nil {
			return applied, err
		}
		if done {
			continue
		}
		body, err := fs.ReadFile(seed.FS, name)
		if err != nil {
			return applied, err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			// No arguments: sent with the simple protocol, so a file can hold several statements
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO seed_run (name) VALUES ($1)`, name)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("seed %s: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}
