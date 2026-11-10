package main

import (
	"context"
	"net"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/rs/zerolog"

	inboundgrpc "api/internal/adapter/inbound/grpc"
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

	// Live events go out through Redis Streams, so open pages refresh without polling:
	// liveService.Notify(ctx, service.EventSystemPing, map[string]string{"id": "..."})
	liveService := service.NewLiveService(redisclient.NewLiveBus(redisClient), logger)
	liveHandler := handler.NewLiveHandler(liveService, logger)

	app := fiber.New(fiber.Config{
		AppName: cfg.AppName,
		// Client IP comes from X-Forwarded-For only when the request arrives from a trusted proxy (joseph-app)
		TrustProxy:         true,
		TrustProxyConfig:   fiber.TrustProxyConfig{Proxies: cfg.TrustedProxies, Loopback: true, Private: true},
		ProxyHeader:        fiber.HeaderXForwardedFor,
		EnableIPValidation: true,
	})

	app.Use(requestid.New())
	app.Use(recover.New())
	app.Use(logging.FiberMiddleware(logger))

	inboundhttp.RegisterRoutes(app, inboundhttp.Handlers{
		Health:           healthHandler,
		Live:             liveHandler,
		WebSocketOrigins: cfg.WSOrigins,
	})

	// gRPC for joseph-app's server, on its own port next to the HTTP API
	grpcListener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logger.Fatal().Err(err).Str("port", cfg.GRPCPort).Msg("failed to open grpc port")
	}
	grpcServer := inboundgrpc.New(inboundgrpc.Services{Health: healthService}, inboundgrpc.Config{TrustedProxies: cfg.TrustedProxies, Development: cfg.AppEnv != "production"}, logger)
	defer grpcServer.GracefulStop()
	go func() {
		logger.Info().Str("port", cfg.GRPCPort).Msg("starting grpc server")
		if err := grpcServer.Serve(grpcListener); err != nil {
			logger.Fatal().Err(err).Msg("grpc server stopped")
		}
	}()

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
