// Migrations binary: applies the pending `migrations/*.up.sql` files and keeps
// track of what ran in `schema_migrations`. It connects with the DB_*
// variables from the .env, the same ones the API uses.
//
// Running `up` twice reapplies nothing: that is why the container entrypoint
// can fail hard instead of swallowing errors with `|| true`.
//
//	migrations up      applies the pending ones, in order
//	migrations down    reverts the last applied one
//	migrations adopt   marks every migration as applied without running it
//
// `adopt` is for a database that was already migrated by hand, from before
// this registry existed.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"api/internal/adapter/logging"
	"api/internal/config"
)

const (
	migrationsDir  = "migrations"
	dbWaitAttempts = 60
	dbWaitInterval = time.Second
	registryDDL    = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    varchar(255) PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`
)

func main() {
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	cfg := config.Load()
	logger := logging.New(cfg)

	ctx := context.Background()

	conn, err := connect(ctx, logger, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to the database")
	}
	defer conn.Close(ctx)

	switch command {
	case "up":
		err = up(ctx, logger, conn)
	case "down":
		err = down(ctx, logger, conn)
	case "adopt":
		err = adopt(ctx, logger, conn)
	default:
		logger.Fatal().Str("command", command).Msg("unknown command: expected up, down or adopt")
	}

	if err != nil {
		logger.Fatal().Err(err).Str("command", command).Msg("migrations failed")
	}
}

// connect retries because the database container may still be starting when
// the API boots next to it, without a depends_on: service_healthy to wait on.
func connect(ctx context.Context, logger zerolog.Logger, dsn string) (*pgx.Conn, error) {
	var (
		conn *pgx.Conn
		err  error
	)

	for attempt := 1; attempt <= dbWaitAttempts; attempt++ {
		conn, err = pgx.Connect(ctx, dsn)
		if err == nil {
			break
		}
		if attempt == 1 {
			logger.Info().Msg("waiting for postgres to answer")
		}
		time.Sleep(dbWaitInterval)
	}

	if err != nil {
		return nil, fmt.Errorf("postgres did not answer after %s: %w", time.Duration(dbWaitAttempts)*dbWaitInterval, err)
	}

	if _, err := conn.Exec(ctx, registryDDL); err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("creating schema_migrations: %w", err)
	}

	return conn, nil
}

// versions lists the files of one direction, sorted, along with their version
// (the file name without the suffix: 0001_init.up.sql -> 0001_init).
func versions(suffix string, reverse bool) ([]string, []string, error) {
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*"+suffix))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	if reverse {
		for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
			files[i], files[j] = files[j], files[i]
		}
	}

	names := make([]string, len(files))
	for i, file := range files {
		names[i] = strings.TrimSuffix(filepath.Base(file), suffix)
	}

	return files, names, nil
}

func applied(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	done := map[string]bool{}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		done[version] = true
	}

	return done, rows.Err()
}

// exec runs the file and updates the registry in a single transaction: if the
// SQL fails the version is not left marked as applied.
func exec(ctx context.Context, conn *pgx.Conn, file, version, registry string) error {
	statements, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(statements)); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	if _, err := tx.Exec(ctx, registry, version); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func up(ctx context.Context, logger zerolog.Logger, conn *pgx.Conn) error {
	files, names, err := versions(".up.sql", false)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		logger.Warn().Str("dir", migrationsDir).Msg("no .up.sql migrations found")
		return nil
	}

	done, err := applied(ctx, conn)
	if err != nil {
		return err
	}

	pending := 0
	for i, file := range files {
		if done[names[i]] {
			continue
		}
		logger.Info().Str("version", names[i]).Msg("applying migration")
		if err := exec(ctx, conn, file, names[i], "INSERT INTO schema_migrations (version) VALUES ($1)"); err != nil {
			return err
		}
		pending++
	}

	if pending == 0 {
		logger.Info().Msg("database already up to date, no pending migrations")
		return nil
	}

	logger.Info().Int("applied", pending).Msg("migrations complete")
	return nil
}

func down(ctx context.Context, logger zerolog.Logger, conn *pgx.Conn) error {
	var last string
	err := conn.QueryRow(ctx, "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		logger.Info().Msg("nothing to revert")
		return nil
	}
	if err != nil {
		return err
	}

	file := filepath.Join(migrationsDir, last+".down.sql")
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("missing %s to revert %s", file, last)
	}

	logger.Info().Str("version", last).Msg("reverting migration")
	if err := exec(ctx, conn, file, last, "DELETE FROM schema_migrations WHERE version = $1"); err != nil {
		return err
	}

	logger.Info().Str("version", last).Msg("migration reverted")
	return nil
}

func adopt(ctx context.Context, logger zerolog.Logger, conn *pgx.Conn) error {
	_, names, err := versions(".up.sql", false)
	if err != nil {
		return err
	}

	done, err := applied(ctx, conn)
	if err != nil {
		return err
	}

	adopted := 0
	for _, version := range names {
		if done[version] {
			continue
		}
		if _, err := conn.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			return err
		}
		logger.Info().Str("version", version).Msg("migration adopted without running")
		adopted++
	}

	if adopted == 0 {
		logger.Info().Msg("nothing to adopt: the registry was already complete")
		return nil
	}

	logger.Info().Int("adopted", adopted).Msg("registry synced with the current database state")
	return nil
}
