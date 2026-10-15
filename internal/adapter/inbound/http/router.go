package http

import (
	"github.com/gofiber/fiber/v3"

	"api/internal/adapter/inbound/http/handler"
)

type Handlers struct {
	Health *handler.HealthHandler
}

func RegisterRoutes(app *fiber.App, h Handlers) {
	v1 := app.Group("/api/v1")
	v1.Get("/health", h.Health.Check)
}
