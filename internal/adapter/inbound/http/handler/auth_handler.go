package handler

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/service"
)

// AuthHandler serves /api/v1/auth/*. joseph-app's server is the only caller: it keeps the session token in an
// httpOnly cookie and sends it here as Authorization: Bearer
type AuthHandler struct {
	auth   *service.AuthService
	logger zerolog.Logger
}

func NewAuthHandler(auth *service.AuthService, logger zerolog.Logger) *AuthHandler {
	return &AuthHandler{auth: auth, logger: logger}
}

var statusByKind = map[domain.Kind]int{
	domain.KindInternal:        fiber.StatusInternalServerError,
	domain.KindInvalid:         fiber.StatusUnprocessableEntity,
	domain.KindUnauthenticated: fiber.StatusUnauthorized,
	domain.KindForbidden:       fiber.StatusForbidden,
	domain.KindNotFound:        fiber.StatusNotFound,
	domain.KindConflict:        fiber.StatusConflict,
	domain.KindLocked:          fiber.StatusLocked,
	domain.KindRateLimited:     fiber.StatusTooManyRequests,
	domain.KindUnavailable:     fiber.StatusServiceUnavailable,
}

// fail answers {"error": {code, message, fields}}; unexpected errors are logged and hidden
func fail(c fiber.Ctx, logger zerolog.Logger, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		logger.Error().Err(err).Str("path", c.Path()).Msg("request failed")
		de = domain.ErrInternal
	}
	body := fiber.Map{"code": de.Code, "message": de.Message}
	if len(de.Fields) > 0 {
		body["fields"] = de.Fields
	}
	return c.Status(statusByKind[de.Kind]).JSON(fiber.Map{"error": body})
}

func meta(c fiber.Ctx) domain.RequestMeta {
	return domain.RequestMeta{IP: c.IP(), UserAgent: c.Get(fiber.HeaderUserAgent)}
}

func bearer(c fiber.Ctx) string {
	token, found := strings.CutPrefix(c.Get(fiber.HeaderAuthorization), "Bearer ")
	if !found {
		return ""
	}
	return strings.TrimSpace(token)
}

func captchaToken(c fiber.Ctx) string { return c.Get("X-Captcha-Token") }

func bind[T any](c fiber.Ctx) (T, error) {
	var body T
	if err := c.Bind().JSON(&body); err != nil {
		return body, &domain.Error{Kind: domain.KindInvalid, Code: "invalid_body", Message: "La solicitud no es válida."}
	}
	return body, nil
}

type signInBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// SignIn checks the password and starts the second step: 200 {challenge_token, method, email, expires_at}
func (h *AuthHandler) SignIn(c fiber.Ctx) error {
	body, err := bind[signInBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	step, err := h.auth.SignIn(c.Context(), service.SignInInput{Email: body.Email, Password: body.Password, CaptchaToken: captchaToken(c)}, meta(c))
	if err != nil {
		return fail(c, h.logger, err)
	}
	return c.JSON(fiber.Map{"challenge_token": step.ChallengeToken, "method": step.Method, "email": step.Email, "expires_at": step.ExpiresAt.UTC().Format(time.RFC3339)})
}

type verifyBody struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code"`
}

// VerifySignIn completes the second step: 200 {session: {token, expires_at}}
func (h *AuthHandler) VerifySignIn(c fiber.Ctx) error {
	body, err := bind[verifyBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	session, err := h.auth.VerifySignIn(c.Context(), body.ChallengeToken, body.Code, meta(c))
	if err != nil {
		return fail(c, h.logger, err)
	}
	return c.JSON(fiber.Map{"session": fiber.Map{"token": session.Token, "expires_at": session.ExpiresAt.UTC().Format(time.RFC3339)}})
}

func (h *AuthHandler) ResendCode(c fiber.Ctx) error {
	body, err := bind[verifyBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	if err := h.auth.ResendSignInCode(c.Context(), body.ChallengeToken); err != nil {
		return fail(c, h.logger, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type signUpBody struct {
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) SignUp(c fiber.Ctx) error {
	body, err := bind[signUpBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	if err := h.auth.SignUp(c.Context(), service.SignUpInput{FullName: body.FullName, Email: body.Email, Password: body.Password, CaptchaToken: captchaToken(c)}, meta(c)); err != nil {
		return fail(c, h.logger, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"ok": true})
}

type forgotBody struct {
	Email string `json:"email"`
}

func (h *AuthHandler) ForgotPassword(c fiber.Ctx) error {
	body, err := bind[forgotBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	if err := h.auth.ForgotPassword(c.Context(), body.Email, captchaToken(c), meta(c)); err != nil {
		return fail(c, h.logger, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"ok": true})
}

type resetBody struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (h *AuthHandler) ResetPassword(c fiber.Ctx) error {
	body, err := bind[resetBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	if err := h.auth.ResetPassword(c.Context(), body.Token, body.Password); err != nil {
		return fail(c, h.logger, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type changeBody struct {
	CurrentPassword string `json:"current_password"`
	Password        string `json:"password"`
}

func (h *AuthHandler) ChangePassword(c fiber.Ctx) error {
	body, err := bind[changeBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	if err := h.auth.ChangePassword(c.Context(), bearer(c), body.CurrentPassword, body.Password); err != nil {
		return fail(c, h.logger, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func roleJSON(r domain.RoleAssignment) fiber.Map {
	out := fiber.Map{"id": r.ID, "code": r.RoleCode, "name": r.RoleName, "scope": r.Scope, "institution": nil}
	if r.Institution != nil {
		out["institution"] = fiber.Map{"id": r.Institution.ID, "slug": r.Institution.Slug, "name": r.Institution.Name, "kind": r.Institution.Kind}
	}
	return out
}

// Me: 200 {user, roles, active_role_id, totp_enabled, session_expires_at}
func (h *AuthHandler) Me(c fiber.Ctx) error {
	me, err := h.auth.Me(c.Context(), bearer(c))
	if err != nil {
		return fail(c, h.logger, err)
	}
	roles := make([]fiber.Map, 0, len(me.Roles))
	for _, r := range me.Roles {
		roles = append(roles, roleJSON(r))
	}
	var activeRoleID *string
	if me.ActiveRole != nil {
		activeRoleID = &me.ActiveRole.ID
	}
	return c.JSON(fiber.Map{
		"user": fiber.Map{
			"id": me.User.ID, "email": me.User.Email, "full_name": me.User.FullName, "avatar_url": me.User.AvatarURL,
			"email_verified": me.User.EmailVerifiedAt != nil,
		},
		"roles":              roles,
		"active_role_id":     activeRoleID,
		"totp_enabled":       me.TOTPEnabled,
		"session_expires_at": me.Session.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

type activeRoleBody struct {
	UserRoleID string `json:"user_role_id"`
}

func (h *AuthHandler) SetActiveRole(c fiber.Ctx) error {
	body, err := bind[activeRoleBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	if err := h.auth.SetActiveRole(c.Context(), bearer(c), body.UserRoleID); err != nil {
		return fail(c, h.logger, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *AuthHandler) SignOut(c fiber.Ctx) error {
	if err := h.auth.SignOut(c.Context(), bearer(c)); err != nil {
		return fail(c, h.logger, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// BeginTOTP: 200 {secret, otpauth_uri}
func (h *AuthHandler) BeginTOTP(c fiber.Ctx) error {
	setup, err := h.auth.BeginTOTP(c.Context(), bearer(c))
	if err != nil {
		return fail(c, h.logger, err)
	}
	return c.JSON(fiber.Map{"secret": setup.Secret, "otpauth_uri": setup.URI})
}

type codeBody struct {
	Code string `json:"code"`
}

// ConfirmTOTP: 200 {recovery_codes}, shown once
func (h *AuthHandler) ConfirmTOTP(c fiber.Ctx) error {
	body, err := bind[codeBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	codes, err := h.auth.ConfirmTOTP(c.Context(), bearer(c), body.Code)
	if err != nil {
		return fail(c, h.logger, err)
	}
	return c.JSON(fiber.Map{"recovery_codes": codes})
}

func (h *AuthHandler) DisableTOTP(c fiber.Ctx) error {
	body, err := bind[codeBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	if err := h.auth.DisableTOTP(c.Context(), bearer(c), body.Code); err != nil {
		return fail(c, h.logger, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type googleAuthorizeBody struct {
	State         string `json:"state"`
	CodeChallenge string `json:"code_challenge"`
}

// GoogleAuthorize: 200 {url} of Google's consent screen
func (h *AuthHandler) GoogleAuthorize(c fiber.Ctx) error {
	body, err := bind[googleAuthorizeBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	url, err := h.auth.GoogleAuthorize(body.State, body.CodeChallenge)
	if err != nil {
		return fail(c, h.logger, err)
	}
	return c.JSON(fiber.Map{"url": url})
}

type googleCallbackBody struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
}

// GoogleCallback: 200 {session: {token, expires_at}}, or the TOTP step {challenge_token, method, email, expires_at}
func (h *AuthHandler) GoogleCallback(c fiber.Ctx) error {
	body, err := bind[googleCallbackBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	result, err := h.auth.GoogleCallback(c.Context(), body.Code, body.CodeVerifier, meta(c))
	if err != nil {
		return fail(c, h.logger, err)
	}
	if result.Step != nil {
		return c.JSON(fiber.Map{"challenge_token": result.Step.ChallengeToken, "method": result.Step.Method, "email": result.Step.Email, "expires_at": result.Step.ExpiresAt.UTC().Format(time.RFC3339)})
	}
	return c.JSON(fiber.Map{"session": fiber.Map{"token": result.Session.Token, "expires_at": result.Session.ExpiresAt.UTC().Format(time.RFC3339)}})
}
