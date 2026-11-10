package http

import (
	"github.com/gofiber/fiber/v3"

	"api/internal/adapter/inbound/http/handler"
)

type Handlers struct {
	Health *handler.HealthHandler
	Live   *handler.LiveHandler
	// Browser origins allowed to open the live WebSocket (joseph-app's origin, through its proxy)
	WebSocketOrigins []string
}

func RegisterRoutes(app *fiber.App, h Handlers) {
	v1 := app.Group("/api/v1")
	v1.Get("/health", h.Health.Check)

	// Live events for open pages (joseph-app relays /ws/live here); events carry ids only
	v1.Get("/live", h.Live.Stream(h.WebSocketOrigins))
}
