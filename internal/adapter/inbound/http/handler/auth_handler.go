package handler

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
	qrcode "github.com/skip2/go-qrcode"

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

// signInJSON: {session: {token, expires_at}}, or the authenticator-app step {challenge_token, method, email, expires_at}
func signInJSON(c fiber.Ctx, result service.SignInResult) error {
	if result.Step != nil {
		return c.JSON(fiber.Map{"challenge_token": result.Step.ChallengeToken, "method": result.Step.Method, "email": result.Step.Email, "expires_at": result.Step.ExpiresAt.UTC().Format(time.RFC3339)})
	}
	return c.JSON(fiber.Map{"session": fiber.Map{"token": result.Session.Token, "expires_at": result.Session.ExpiresAt.UTC().Format(time.RFC3339)}})
}

// SignIn checks the password: a session, or the authenticator-app step when the account has it on
func (h *AuthHandler) SignIn(c fiber.Ctx) error {
	body, err := bind[signInBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	result, err := h.auth.SignIn(c.Context(), service.SignInInput{Email: body.Email, Password: body.Password, CaptchaToken: captchaToken(c)}, meta(c))
	if err != nil {
		return fail(c, h.logger, err)
	}
	return signInJSON(c, result)
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

func isoTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.UTC().Format(time.RFC3339)
	return &v
}

// Me: 200 {user, roles, active_role_id, totp_enabled, google_linked, password_changed_at, last_sign_in_at, ...}
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
			"has_password":   me.User.PasswordHash != "",
			"created_at":     me.User.CreatedAt.UTC().Format(time.RFC3339),
		},
		"password_changed_at": isoTime(me.User.PasswordChangedAt),
		"last_sign_in_at":     isoTime(me.User.LastSignInAt),
		"google_linked":       me.GoogleLinked,
		"roles":               roles,
		"active_role_id":      activeRoleID,
		"totp_enabled":        me.TOTPEnabled,
		"session_expires_at":  me.Session.ExpiresAt.UTC().Format(time.RFC3339),
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

// MFAStatus: 200 {totp_enabled, enabled_at, recovery_codes: {total, remaining, used: [{code, used_at}]}}
func (h *AuthHandler) MFAStatus(c fiber.Ctx) error {
	status, err := h.auth.MFAStatus(c.Context(), bearer(c))
	if err != nil {
		return fail(c, h.logger, err)
	}
	used := make([]fiber.Map, 0, len(status.RecoveryUsed))
	for _, code := range status.RecoveryUsed {
		used = append(used, fiber.Map{"code": code.UsedCode, "used_at": isoTime(code.UsedAt)})
	}
	return c.JSON(fiber.Map{
		"totp_enabled":   status.TOTPEnabled,
		"enabled_at":     isoTime(status.EnabledAt),
		"recovery_codes": fiber.Map{"total": status.RecoveryTotal, "remaining": status.RecoveryRemaining, "used": used},
	})
}

type mfaCodeRequestBody struct {
	// mfa_enable | mfa_disable | mfa_recovery_codes
	Purpose string `json:"purpose"`
}

// SendMFACode mails the code that confirms a change: 200 {challenge_token, email, expires_at}
func (h *AuthHandler) SendMFACode(c fiber.Ctx) error {
	body, err := bind[mfaCodeRequestBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	sent, err := h.auth.SendMFACode(c.Context(), bearer(c), body.Purpose)
	if err != nil {
		return fail(c, h.logger, err)
	}
	return c.JSON(fiber.Map{"challenge_token": sent.ChallengeToken, "email": sent.Email, "expires_at": sent.ExpiresAt.UTC().Format(time.RFC3339)})
}

// mailedCodeBody carries the mailed code and the challenge it belongs to
type mailedCodeBody struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code"`
}

// BeginTOTP: 200 {secret, otpauth_uri, qr_png} once the mailed code is confirmed; qr_png is a data: URL
func (h *AuthHandler) BeginTOTP(c fiber.Ctx) error {
	body, err := bind[mailedCodeBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	setup, err := h.auth.BeginTOTP(c.Context(), bearer(c), body.ChallengeToken, body.Code)
	if err != nil {
		return fail(c, h.logger, err)
	}
	png, err := qrcode.Encode(setup.URI, qrcode.Medium, 256)
	if err != nil {
		return fail(c, h.logger, err)
	}
	return c.JSON(fiber.Map{"secret": setup.Secret, "otpauth_uri": setup.URI, "qr_png": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)})
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

// DisableTOTP with the mailed code: 204
func (h *AuthHandler) DisableTOTP(c fiber.Ctx) error {
	body, err := bind[mailedCodeBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	if err := h.auth.DisableTOTP(c.Context(), bearer(c), body.ChallengeToken, body.Code); err != nil {
		return fail(c, h.logger, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// RegenerateRecoveryCodes with the mailed code: 200 {recovery_codes}, shown once
func (h *AuthHandler) RegenerateRecoveryCodes(c fiber.Ctx) error {
	body, err := bind[mailedCodeBody](c)
	if err != nil {
		return fail(c, h.logger, err)
	}
	codes, err := h.auth.RegenerateRecoveryCodes(c.Context(), bearer(c), body.ChallengeToken, body.Code)
	if err != nil {
		return fail(c, h.logger, err)
	}
	return c.JSON(fiber.Map{"recovery_codes": codes})
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
	return signInJSON(c, result)
}
