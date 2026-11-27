package http

import (
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"api/internal/adapter/inbound/http/handler"
)

type Handlers struct {
	Health *handler.HealthHandler
	Auth   *handler.AuthHandler
	// Announcements: publishing and files (the feeds are read over gRPC)
	Announcements *handler.AnnouncementHandler
	// Attachments: files (PDF, images, Word, Excel) hung on any record of an institution
	Attachments *handler.AttachmentHandler
	Live        *handler.LiveHandler
	// REST: the grpc-gateway of rest/http.yaml (/api/v1/institutions/...), its OpenAPI document and Swagger UI page
	REST      http.Handler
	OpenAPI   []byte
	SwaggerUI string
	// Browser origins allowed to open the live WebSocket (lt-edu-app's origin, through its proxy)
	WebSocketOrigins []string
	// Development relaxes the per-IP brake, for testing
	Development bool
}

func RegisterRoutes(app *fiber.App, h Handlers) {
	v1 := app.Group("/api/v1")
	v1.Get("/health", h.Health.Check)

	// Brute-force brake per client IP on everything that takes a password or a code (the per-account lockout and
	// the per-challenge attempt budget live in the service)
	credentialMax := 10
	if h.Development {
		credentialMax = 100
	}
	credentialLimit := limiter.New(limiter.Config{
		Max:          credentialMax,
		Expiration:   time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return c.IP() },
		LimitReached: func(c fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": fiber.Map{"code": "rate_limited", "message": "Demasiados intentos desde tu conexión. Espera un minuto."}})
		},
	})

	auth := v1.Group("/auth")
	auth.Post("/sign-in", credentialLimit, h.Auth.SignIn)
	auth.Post("/sign-in/verify", credentialLimit, h.Auth.VerifySignIn)
	auth.Post("/sign-up", credentialLimit, h.Auth.SignUp)
	auth.Post("/google/authorize", credentialLimit, h.Auth.GoogleAuthorize)
	auth.Post("/google/callback", credentialLimit, h.Auth.GoogleCallback)
	auth.Post("/password/forgot", credentialLimit, h.Auth.ForgotPassword)
	auth.Post("/password/reset", credentialLimit, h.Auth.ResetPassword)
	auth.Post("/password/change", credentialLimit, h.Auth.ChangePassword)
	auth.Get("/me", h.Auth.Me)
	auth.Put("/me/active-role", h.Auth.SetActiveRole)
	auth.Post("/sign-out", h.Auth.SignOut)
	// Two-step verification (Mi cuenta): every change is confirmed with a code mailed to the account
	auth.Get("/mfa", h.Auth.MFAStatus)
	auth.Post("/mfa/email-code", credentialLimit, h.Auth.SendMFACode)
	auth.Post("/mfa/totp", credentialLimit, h.Auth.BeginTOTP)
	auth.Post("/mfa/totp/confirm", credentialLimit, h.Auth.ConfirmTOTP)
	auth.Post("/mfa/totp/disable", credentialLimit, h.Auth.DisableTOTP)
	auth.Post("/mfa/recovery-codes", credentialLimit, h.Auth.RegenerateRecoveryCodes)

	// Announcements with images and PDFs: publishing from the panel, and the files (public ones for anyone)
	v1.Post("/panel/announcements", credentialLimit, h.Announcements.Create)
	v1.Put("/panel/announcements/:id", credentialLimit, h.Announcements.Update)
	v1.Delete("/panel/announcements/:id", credentialLimit, h.Announcements.Delete)
	v1.Get("/announcements/:id/attachments/:attachmentId", h.Announcements.Attachment)

	// Generic attachments of any record (enrollment, student, staff, period, acta...): checked per owner module
	v1.Get("/panel/attachments", h.Attachments.List)
	v1.Post("/panel/attachments", credentialLimit, h.Attachments.Upload)
	v1.Delete("/panel/attachments/:id", credentialLimit, h.Attachments.Delete)
	v1.Get("/attachments/:id", h.Attachments.File)

	// Live events for open pages (lt-edu-app relays /ws/live here); events carry ids only
	v1.Get("/live", h.Live.Stream(h.WebSocketOrigins))

	// REST over the same core services as gRPC: every route hangs from the institution
	if h.REST != nil {
		v1.Use("/institutions", adaptor.HTTPHandler(h.REST))
		app.Get("/api/docs/openapi.json", func(c fiber.Ctx) error {
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			return c.Send(h.OpenAPI)
		})
		app.Get("/api/docs", func(c fiber.Ctx) error {
			c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
			return c.SendString(h.SwaggerUI)
		})
	}
}
