package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/rs/zerolog"

	"api/internal/adapter/logging"
	"api/internal/adapter/outbound/postgres"
	"api/internal/config"
)

const (
	usage         = "usage: migrate <up|down|version|force VERSION|create NAME>"
	migrationsDir = "migrations"
)

var migrationName = regexp.MustCompile(`^[a-z0-9_]+$`)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	cfg := config.Load()
	logger := logging.New(cfg)

	// create only writes files, it never touches the database
	if os.Args[1] == "create" {
		if len(os.Args) < 3 || !migrationName.MatchString(os.Args[2]) {
			fmt.Fprintln(os.Stderr, "migration name must be snake_case, e.g. create_institutions")
			os.Exit(2)
		}
		if err := create(os.Args[2], logger); err != nil {
			logger.Fatal().Err(err).Msg("failed to create migration")
		}
		return
	}

	migrator, err := postgres.NewMigrator(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize migrator")
	}
	defer migrator.Close()

	switch os.Args[1] {
	case "up":
		changed, err := migrator.Up()
		if err != nil {
			logger.Fatal().Err(err).Msg("migrate up failed")
		}
		logVersion(logger, migrator, changed, "migrated up")
	case "down":
		changed, err := migrator.Down()
		if err != nil {
			logger.Fatal().Err(err).Msg("migrate down failed")
		}
		logVersion(logger, migrator, changed, "rolled back one migration")
	case "version":
		logVersion(logger, migrator, true, "current version")
	case "force":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		version, err := strconv.Atoi(os.Args[2])
		if err != nil {
			logger.Fatal().Err(err).Msg("version must be a number")
		}
		if err := migrator.Force(version); err != nil {
			logger.Fatal().Err(err).Msg("migrate force failed")
		}
		logVersion(logger, migrator, true, "version forced")
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func logVersion(logger zerolog.Logger, migrator *postgres.Migrator, changed bool, msg string) {
	if !changed {
		msg = "no change"
	}
	version, dirty, err := migrator.Version()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to read migration version")
	}
	logger.Info().Uint("version", version).Bool("dirty", dirty).Msg(msg)
}

// create writes the next NNNN_name.up.sql / NNNN_name.down.sql pair
func create(name string, logger zerolog.Logger) error {
	existing, err := filepath.Glob(filepath.Join(migrationsDir, "*.up.sql"))
	if err != nil {
		return err
	}

	next := 1
	for _, path := range existing {
		var version int
		if _, err := fmt.Sscanf(filepath.Base(path), "%d_", &version); err == nil && version >= next {
			next = version + 1
		}
	}

	base := filepath.Join(migrationsDir, fmt.Sprintf("%04d_%s", next, name))
	for _, suffix := range []string{".up.sql", ".down.sql"} {
		file, err := os.OpenFile(base+suffix, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		logger.Info().Str("file", base+suffix).Msg("created")
	}
	return nil
}
