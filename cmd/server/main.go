package main

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/rs/zerolog"

	inboundhttp "api/internal/adapter/inbound/http"
	"api/internal/adapter/inbound/http/handler"
	"api/internal/adapter/logging"
	"api/internal/adapter/outbound/postgres"
	"api/internal/adapter/outbound/redisclient"
	"api/internal/config"
	"api/internal/core/port"
	"api/internal/core/service"
)

func main() {
	cfg := config.Load()
	logger := logging.New(cfg)

	dbClient, err := postgres.New(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize database pool")
	}
	defer dbClient.Close()

	redisClient := redisclient.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, logger)
	defer redisClient.Close()

	logStartupConnection(logger, "database", dbClient, cfg.PingTimeout)
	logStartupConnection(logger, "redis", redisClient, cfg.PingTimeout)

	healthService := service.NewHealthService(dbClient, redisClient, cfg.AppName, cfg.PingTimeout)
	healthHandler := handler.NewHealthHandler(healthService, logger)

	app := fiber.New(fiber.Config{
		AppName: cfg.AppName,
	})

	app.Use(requestid.New())
	app.Use(recover.New())
	app.Use(logging.FiberMiddleware(logger))

	inboundhttp.RegisterRoutes(app, inboundhttp.Handlers{
		Health: healthHandler,
	})

	logger.Info().Str("port", cfg.Port).Str("env", cfg.AppEnv).Msg("starting server")

	if err := app.Listen(":" + cfg.Port); err != nil {
		logger.Fatal().Err(err).Msg("server stopped")
	}
}

func logStartupConnection(logger zerolog.Logger, name string, pinger port.Pinger, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := pinger.Ping(ctx); err != nil {
		logger.Warn().Str("component", name).Err(err).Msg("connection check failed at startup")
		return false
	}

	logger.Info().Str("component", name).Msg("connection established")
	return true
}
