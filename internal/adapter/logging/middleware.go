package logging

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/rs/zerolog"
)

func FiberMiddleware(log zerolog.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()

		err := c.Next()

		if err != nil {
			if handlerErr := c.App().ErrorHandler(c, err); handlerErr != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}

		status := c.Response().StatusCode()
		latency := time.Since(start)

		event := log.Info()
		switch {
		case err != nil || status >= fiber.StatusInternalServerError:
			event = log.Error()
		case status >= fiber.StatusBadRequest:
			event = log.Warn()
		}

		event.
			Str("request_id", requestid.FromContext(c)).
			Str("method", c.Method()).
			Str("path", c.Path()).
			Int("status", status).
			Dur("latency", latency).
			Str("ip", c.IP()).
			Str("user_agent", c.Get(fiber.HeaderUserAgent)).
			Int("bytes_out", len(c.Response().Body())).
			AnErr("error", err).
			Msg("http_request")

		return nil
	}
}
