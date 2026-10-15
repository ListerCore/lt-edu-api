package handler

import (
	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/service"
)

type HealthHandler struct {
	healthService *service.HealthService
	logger        zerolog.Logger
}

func NewHealthHandler(healthService *service.HealthService, logger zerolog.Logger) *HealthHandler {
	return &HealthHandler{healthService: healthService, logger: logger}
}

func (h *HealthHandler) Check(c fiber.Ctx) error {
	opts := service.CheckOptions{
		IncludeDB:    fiber.Query[bool](c, "db", false),
		IncludeRedis: fiber.Query[bool](c, "redis", false),
	}

	health := h.healthService.Check(c.Context(), c.IP(), opts, h.logger)

	status := fiber.StatusOK
	if health.Status != domain.StatusOK {
		status = fiber.StatusServiceUnavailable
	}

	return c.Status(status).JSON(health)
}
