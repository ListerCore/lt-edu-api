// Command seed loads the data the platform starts with into a migrated database: the files of migrations/seed
// (each runs once; running it again only applies files added since), then the superadmin of APP__ADMIN__*.
// The Docker entrypoint runs it after migrate up, before the server starts.
package main

import (
	"context"
	"time"

	"api/internal/adapter/logging"
	"api/internal/adapter/outbound/password"
	"api/internal/adapter/outbound/postgres"
	"api/internal/config"
	"api/internal/core/service"
)

func main() {
	cfg := config.Load()
	logger := logging.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	seeder, err := postgres.NewSeeder(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize seeder")
	}
	defer seeder.Close()

	applied, err := seeder.Run(ctx)
	for _, name := range applied {
		logger.Info().Str("file", name).Msg("seeded")
	}
	if err != nil {
		logger.Fatal().Err(err).Msg("seed failed")
	}
	if len(applied) == 0 {
		logger.Info().Msg("seed files: no change")
	}

	// The superadmin needs an Argon2id hash, so it is seeded from Go rather than SQL. An existing account keeps
	// its password; the seed only guarantees the account and its role
	db, err := postgres.New(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize database pool")
	}
	defer db.Close()
	auth, err := service.NewAuthService(postgres.NewAuthRepository(db), password.NewArgon2id(), nil, nil, nil, nil, service.AuthConfig{}, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize auth service")
	}
	if cfg.AdminEmail == "" {
		logger.Warn().Msg("APP__ADMIN__EMAIL is empty: no superadmin seeded")
		return
	}
	if err := auth.EnsureSuperadmin(ctx, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		logger.Fatal().Err(err).Msg("superadmin seed failed")
	}
	logger.Info().Str("email", cfg.AdminEmail).Msg("superadmin ready")
}
