package domain

import "errors"

// Kind classifies an error for the transports (HTTP status, gRPC code)
type Kind int

const (
	KindInternal Kind = iota
	KindInvalid
	KindUnauthenticated
	KindForbidden
	KindNotFound
	KindConflict
	KindLocked
	KindRateLimited
	KindUnavailable
)

// Error is a failure the caller is meant to see: stable code plus a message ready to show (Spanish)
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fields  map[string]string
}

func (e *Error) Error() string { return e.Code }

// Is matches by code, so errors.Is(err, ErrInvalidCode) works on copies too
func (e *Error) Is(target error) bool {
	var t *Error
	return errors.As(target, &t) && t.Code == e.Code
}

func newError(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// Validation builds a per-field error for a form
func Validation(fields map[string]string) *Error {
	return &Error{Kind: KindInvalid, Code: "validation", Message: "Revisa los datos del formulario.", Fields: fields}
}

var (
	ErrInternal             = newError(KindInternal, "internal", "Ocurrió un error inesperado. Inténtalo de nuevo.")
	ErrNotFound             = newError(KindNotFound, "not_found", "No encontramos lo que buscas.")
	ErrCaptchaFailed        = newError(KindInvalid, "captcha_failed", "No pudimos verificar que no eres un robot. Inténtalo de nuevo.")
	ErrInvalidCredentials   = newError(KindUnauthenticated, "invalid_credentials", "Correo o contraseña incorrectos.")
	ErrWrongPassword        = newError(KindInvalid, "invalid_credentials", "La contraseña actual no es correcta.")
	ErrAccountLocked        = newError(KindLocked, "account_locked", "Tu cuenta quedó bloqueada por varios intentos fallidos. Inténtalo en 15 minutos.")
	ErrUnauthenticated      = newError(KindUnauthenticated, "unauthenticated", "Tu sesión venció. Ingresa de nuevo.")
	ErrChallengeExpired     = newError(KindUnauthenticated, "expired_challenge", "La verificación venció. Ingresa de nuevo.")
	ErrInvalidCode          = newError(KindInvalid, "invalid_code", "El código no es válido o ya venció.")
	ErrResendTooSoon        = newError(KindRateLimited, "resend_too_soon", "Espera unos segundos antes de pedir otro código.")
	ErrExpiredLink          = newError(KindInvalid, "expired_link", "El enlace venció. Solicita uno nuevo.")
	ErrEmailTaken           = newError(KindConflict, "email_taken", "Ya existe una cuenta con ese correo.")
	ErrNotInvited           = newError(KindForbidden, "not_invited", "Tu correo no figura en ninguna institución. Pide a tu institución que te registre.")
	ErrRoleNotAssigned      = newError(KindForbidden, "role_not_assigned", "No tienes ese rol asignado.")
	ErrTOTPAlreadyEnabled   = newError(KindConflict, "totp_enabled", "La app autenticadora ya está activa.")
	ErrTOTPNotEnrolled      = newError(KindNotFound, "totp_not_enrolled", "Primero configura la app autenticadora.")
	ErrSecretsNotConfigured = newError(KindUnavailable, "secrets_not_configured", "La verificación con app autenticadora no está disponible por ahora.")
	ErrGoogleFailed         = newError(KindUnauthenticated, "google_failed", "No pudimos ingresar con Google. Inténtalo de nuevo o usa tu correo.")
	ErrGoogleNotConfigured  = newError(KindUnavailable, "google_not_configured", "El ingreso con Google no está disponible por ahora.")
	ErrMailUnavailable      = newError(KindUnavailable, "mail_unavailable", "No pudimos enviar el correo. Inténtalo de nuevo en unos minutos.")
)
