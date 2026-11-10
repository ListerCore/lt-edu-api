package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/port"
)

const (
	maxFailedSignIns   = 5
	lockoutDuration    = 15 * time.Minute
	signInChallengeTTL = 10 * time.Minute
	resetLinkTTL       = time.Hour
	maxCodeAttempts    = 5
	resendCooldown     = 30 * time.Second
	// last_seen_at is written at most this often per session
	sessionTouchEvery = 5 * time.Minute
	recoveryCodeCount = 10
	totpIssuer        = "Joseph"
)

type AuthConfig struct {
	SessionTTL time.Duration
	// Public URL of joseph-app, for the links in mail (https://demo.listercore.es)
	SiteURL    string
	Production bool
}

// AuthService runs every account flow: sign-in with a second step (email code or TOTP), sign-up from the
// institution's roster, password reset and change, sessions and roles
type AuthService struct {
	repo    port.AuthRepository
	hasher  port.PasswordHasher
	captcha port.CaptchaVerifier
	secrets port.SecretBox
	mailer  port.Mailer
	google  port.GoogleOAuth
	cfg     AuthConfig
	logger  zerolog.Logger
	// Verified against unknown emails so a missing account costs the same time as a wrong password
	dummyHash string
	now       func() time.Time
}

func NewAuthService(repo port.AuthRepository, hasher port.PasswordHasher, captcha port.CaptchaVerifier, secrets port.SecretBox, mailer port.Mailer, google port.GoogleOAuth, cfg AuthConfig, logger zerolog.Logger) (*AuthService, error) {
	dummy, err := hasher.Hash(randomToken())
	if err != nil {
		return nil, err
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 7 * 24 * time.Hour
	}
	cfg.SiteURL = strings.TrimSuffix(cfg.SiteURL, "/")
	return &AuthService{repo: repo, hasher: hasher, captcha: captcha, secrets: secrets, mailer: mailer, google: google, cfg: cfg, logger: logger, dummyHash: dummy, now: time.Now}, nil
}

// --- Sign-in ---------------------------------------------------------------------------------------------------

type SignInInput struct {
	Email        string
	Password     string
	CaptchaToken string
}

// SignInStep is the second step the browser must complete with ChallengeToken
type SignInStep struct {
	ChallengeToken string
	// email | totp
	Method    string
	Email     string
	ExpiresAt time.Time
}

func (s *AuthService) SignIn(ctx context.Context, in SignInInput, meta domain.RequestMeta) (SignInStep, error) {
	email := normalizeEmail(in.Email)
	if err := s.captcha.Verify(ctx, in.CaptchaToken, meta.IP); err != nil {
		return SignInStep{}, domain.ErrCaptchaFailed
	}
	if fields := requireFields(map[string]string{"email": email, "password": in.Password}); fields != nil {
		return SignInStep{}, fields
	}

	user, err := s.repo.UserByEmail(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		_, _ = s.hasher.Verify(in.Password, s.dummyHash)
		return SignInStep{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return SignInStep{}, err
	}
	if user.LockedUntil != nil && user.LockedUntil.After(s.now()) {
		return SignInStep{}, domain.ErrAccountLocked
	}
	if user.PasswordHash == "" {
		_, _ = s.hasher.Verify(in.Password, s.dummyHash)
		return SignInStep{}, domain.ErrInvalidCredentials
	}
	ok, err := s.hasher.Verify(in.Password, user.PasswordHash)
	if err != nil {
		return SignInStep{}, err
	}
	if !ok {
		if err := s.repo.RecordFailedSignIn(ctx, user.ID, maxFailedSignIns, lockoutDuration); err != nil {
			return SignInStep{}, err
		}
		return SignInStep{}, domain.ErrInvalidCredentials
	}
	// Only after the right password, so a suspended account is not revealed to someone guessing
	if user.Status != domain.UserActive {
		return SignInStep{}, domain.ErrInvalidCredentials
	}

	method := domain.MFAEmail
	if factor, err := s.repo.TOTP(ctx, user.ID); err == nil && factor.ConfirmedAt != nil {
		method = domain.MFATOTP
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return SignInStep{}, err
	}

	// One open sign-in per account: a new one replaces any earlier challenge
	if err := s.repo.CloseChallenges(ctx, user.ID, domain.PurposeSignIn); err != nil {
		return SignInStep{}, err
	}
	token := randomToken()
	now := s.now()
	challenge := domain.Challenge{UserID: user.ID, Purpose: domain.PurposeSignIn, TokenHash: hashToken(token), Method: method, IP: meta.IP, ExpiresAt: now.Add(signInChallengeTTL)}
	var code string
	if method == domain.MFAEmail {
		code = randomDigits(6)
		challenge.CodeHash = hashCode(token, code)
		challenge.CodeSentAt = &now
	}
	if _, err := s.repo.CreateChallenge(ctx, challenge); err != nil {
		return SignInStep{}, err
	}
	if method == domain.MFAEmail {
		if err := s.mailer.Send(ctx, signInCodeMail(user, code)); err != nil {
			s.logger.Error().Err(err).Str("user_id", user.ID).Msg("sign-in code mail failed")
			return SignInStep{}, domain.ErrMailUnavailable
		}
	}
	return SignInStep{ChallengeToken: token, Method: method, Email: user.Email, ExpiresAt: challenge.ExpiresAt}, nil
}

// NewSessionResult carries the session token the app keeps in its httpOnly cookie
type NewSessionResult struct {
	Token     string
	ExpiresAt time.Time
}

// VerifySignIn completes the second step. Code is the 6 digits (email or app) or a recovery code
func (s *AuthService) VerifySignIn(ctx context.Context, challengeToken, code string, meta domain.RequestMeta) (NewSessionResult, error) {
	challenge, err := s.repo.OpenChallenge(ctx, hashToken(challengeToken), domain.PurposeSignIn)
	if errors.Is(err, domain.ErrNotFound) {
		return NewSessionResult{}, domain.ErrChallengeExpired
	}
	if err != nil {
		return NewSessionResult{}, err
	}
	attempts, err := s.repo.CountAttempt(ctx, challenge.ID)
	if err != nil {
		return NewSessionResult{}, err
	}
	if attempts > maxCodeAttempts {
		_, _ = s.repo.ConsumeChallenge(ctx, challenge.ID)
		return NewSessionResult{}, domain.ErrChallengeExpired
	}

	code = strings.TrimSpace(code)
	var method string
	switch challenge.Method {
	case domain.MFAEmail:
		if subtle.ConstantTimeCompare(hashCode(challengeToken, code), challenge.CodeHash) != 1 {
			return NewSessionResult{}, domain.ErrInvalidCode
		}
		method = domain.MFAEmail
	case domain.MFATOTP:
		method, err = s.checkSecondFactor(ctx, challenge.UserID, code)
		if err != nil {
			return NewSessionResult{}, err
		}
	default:
		return NewSessionResult{}, domain.ErrChallengeExpired
	}

	if consumed, err := s.repo.ConsumeChallenge(ctx, challenge.ID); err != nil {
		return NewSessionResult{}, err
	} else if !consumed {
		return NewSessionResult{}, domain.ErrChallengeExpired
	}
	if method == domain.MFAEmail {
		// Receiving the code proves the mailbox belongs to the person
		if err := s.repo.MarkEmailVerified(ctx, challenge.UserID); err != nil {
			return NewSessionResult{}, err
		}
	}
	return s.startSession(ctx, challenge.UserID, method, meta)
}

// checkSecondFactor accepts a TOTP code from the app or one of the recovery codes
func (s *AuthService) checkSecondFactor(ctx context.Context, userID, code string) (string, error) {
	factor, err := s.repo.TOTP(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && factor.ConfirmedAt == nil) {
		return "", domain.ErrTOTPNotEnrolled
	}
	if err != nil {
		return "", err
	}
	if len(code) == totpDigits {
		secret, err := s.totpSecret(factor)
		if err != nil {
			return "", err
		}
		step, ok := totpMatch(secret, code, s.now())
		if !ok {
			return "", domain.ErrInvalidCode
		}
		fresh, err := s.repo.UseTOTPStep(ctx, factor.ID, step)
		if err != nil {
			return "", err
		}
		if !fresh {
			return "", domain.ErrInvalidCode
		}
		return domain.MFATOTP, nil
	}
	used, err := s.repo.UseRecoveryCode(ctx, userID, hashRecoveryCode(code))
	if err != nil {
		return "", err
	}
	if !used {
		return "", domain.ErrInvalidCode
	}
	return domain.MFARecoveryCode, nil
}

// ResendSignInCode mails a new code for an email challenge, at most once per cooldown
func (s *AuthService) ResendSignInCode(ctx context.Context, challengeToken string) error {
	challenge, err := s.repo.OpenChallenge(ctx, hashToken(challengeToken), domain.PurposeSignIn)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrChallengeExpired
	}
	if err != nil {
		return err
	}
	if challenge.Method != domain.MFAEmail {
		return domain.ErrInvalidCode
	}
	if challenge.CodeSentAt != nil && s.now().Sub(*challenge.CodeSentAt) < resendCooldown {
		return domain.ErrResendTooSoon
	}
	user, err := s.repo.UserByID(ctx, challenge.UserID)
	if err != nil {
		return err
	}
	code := randomDigits(6)
	if err := s.repo.ReplaceCode(ctx, challenge.ID, hashCode(challengeToken, code)); err != nil {
		return err
	}
	if err := s.mailer.Send(ctx, signInCodeMail(user, code)); err != nil {
		s.logger.Error().Err(err).Str("user_id", user.ID).Msg("sign-in code resend failed")
		return domain.ErrMailUnavailable
	}
	return nil
}

func (s *AuthService) startSession(ctx context.Context, userID, method string, meta domain.RequestMeta) (NewSessionResult, error) {
	roles, err := s.repo.Roles(ctx, userID)
	if err != nil {
		return NewSessionResult{}, err
	}
	// First role in the list (platform roles first); the person can switch at any time
	var active *string
	if len(roles) > 0 {
		active = &roles[0].ID
	}
	token := randomToken()
	expires := s.now().Add(s.cfg.SessionTTL)
	if _, err := s.repo.CompleteSignIn(ctx, port.NewSession{UserID: userID, TokenHash: hashToken(token), ActiveUserRoleID: active, MFAMethod: method, IP: meta.IP, UserAgent: meta.UserAgent, ExpiresAt: expires}); err != nil {
		return NewSessionResult{}, err
	}
	return NewSessionResult{Token: token, ExpiresAt: expires}, nil
}

// --- Google ----------------------------------------------------------------------------------------------------

var base64URL43 = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// GoogleAuthorize returns Google's consent URL. joseph-app generates state and the PKCE verifier, keeps both in
// httpOnly cookies and sends only the state and the S256 challenge here
func (s *AuthService) GoogleAuthorize(state, codeChallenge string) (string, error) {
	if !s.google.Configured() {
		return "", domain.ErrGoogleNotConfigured
	}
	if !base64URL43.MatchString(state) || !base64URL43.MatchString(codeChallenge) {
		return "", domain.ErrGoogleFailed
	}
	return s.google.AuthorizationURL(state, codeChallenge), nil
}

// GoogleResult is either a session, or the TOTP step when the account has an authenticator app
type GoogleResult struct {
	Session *NewSessionResult
	Step    *SignInStep
}

// GoogleCallback trades the code for the Google account and signs in the matching Joseph account: the one already
// linked, else the one with that verified email (linked now), else a new one from the institution's roster
func (s *AuthService) GoogleCallback(ctx context.Context, code, codeVerifier string, meta domain.RequestMeta) (GoogleResult, error) {
	if !s.google.Configured() {
		return GoogleResult{}, domain.ErrGoogleNotConfigured
	}
	if code == "" || !base64URL43.MatchString(codeVerifier) {
		return GoogleResult{}, domain.ErrGoogleFailed
	}
	identity, err := s.google.Exchange(ctx, code, codeVerifier)
	if errors.Is(err, domain.ErrGoogleNotConfigured) {
		return GoogleResult{}, err
	}
	if err != nil {
		s.logger.Warn().Err(err).Msg("google exchange failed")
		return GoogleResult{}, domain.ErrGoogleFailed
	}

	user, err := s.repo.UserByIdentity(ctx, identity.Provider, identity.Subject)
	switch {
	case err == nil:
		err = s.repo.LinkIdentity(ctx, user.ID, identity)
	case errors.Is(err, domain.ErrNotFound):
		user, err = s.repo.UserByEmail(ctx, identity.Email)
		if err == nil {
			err = s.repo.LinkIdentity(ctx, user.ID, identity)
		} else if errors.Is(err, domain.ErrNotFound) {
			fullName := strings.Join(strings.Fields(identity.FullName), " ")
			if utf8.RuneCountInString(fullName) < 3 {
				fullName = identity.Email[:strings.Index(identity.Email, "@")]
			}
			var userID string
			userID, err = s.repo.CreateUserFromInvitations(ctx, identity.Email, fullName, "", &identity)
			if err == nil {
				s.logger.Info().Str("user_id", userID).Msg("account created from invitations with google")
				user, err = s.repo.UserByID(ctx, userID)
			}
		}
	}
	if err != nil {
		return GoogleResult{}, err
	}
	if user.Status != domain.UserActive {
		return GoogleResult{}, domain.ErrGoogleFailed
	}

	// An authenticator app is still asked for: Google proves the mailbox, not the second factor the person chose
	if factor, err := s.repo.TOTP(ctx, user.ID); err == nil && factor.ConfirmedAt != nil {
		if err := s.repo.CloseChallenges(ctx, user.ID, domain.PurposeSignIn); err != nil {
			return GoogleResult{}, err
		}
		token := randomToken()
		expires := s.now().Add(signInChallengeTTL)
		if _, err := s.repo.CreateChallenge(ctx, domain.Challenge{UserID: user.ID, Purpose: domain.PurposeSignIn, TokenHash: hashToken(token), Method: domain.MFATOTP, IP: meta.IP, ExpiresAt: expires}); err != nil {
			return GoogleResult{}, err
		}
		return GoogleResult{Step: &SignInStep{ChallengeToken: token, Method: domain.MFATOTP, Email: user.Email, ExpiresAt: expires}}, nil
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return GoogleResult{}, err
	}

	session, err := s.startSession(ctx, user.ID, domain.MFAGoogle, meta)
	if err != nil {
		return GoogleResult{}, err
	}
	return GoogleResult{Session: &session}, nil
}

// --- Sign-up ---------------------------------------------------------------------------------------------------

type SignUpInput struct {
	FullName     string
	Email        string
	Password     string
	CaptchaToken string
}

// SignUp creates the account of someone an institution registered; the roster decides institution and roles
func (s *AuthService) SignUp(ctx context.Context, in SignUpInput, meta domain.RequestMeta) error {
	if err := s.captcha.Verify(ctx, in.CaptchaToken, meta.IP); err != nil {
		return domain.ErrCaptchaFailed
	}
	email := normalizeEmail(in.Email)
	fullName := strings.Join(strings.Fields(in.FullName), " ")
	fields := map[string]string{}
	if utf8.RuneCountInString(fullName) < 3 || utf8.RuneCountInString(fullName) > 200 {
		fields["full_name"] = "Ingresa tu nombre completo."
	}
	if !validEmail(email) {
		fields["email"] = "Ingresa un correo válido."
	}
	if msg := passwordProblem(in.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return err
	}
	userID, err := s.repo.CreateUserFromInvitations(ctx, email, fullName, hash, nil)
	if err != nil {
		return err
	}
	s.logger.Info().Str("user_id", userID).Msg("account created from invitations")
	return nil
}

// --- Passwords -------------------------------------------------------------------------------------------------

// ForgotPassword mails a reset link when the account exists. It always succeeds, so it never reveals which
// emails have an account
func (s *AuthService) ForgotPassword(ctx context.Context, email, captchaToken string, meta domain.RequestMeta) error {
	if err := s.captcha.Verify(ctx, captchaToken, meta.IP); err != nil {
		return domain.ErrCaptchaFailed
	}
	email = normalizeEmail(email)
	if !validEmail(email) {
		return domain.Validation(map[string]string{"email": "Ingresa un correo válido."})
	}
	user, err := s.repo.UserByEmail(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if user.Status != domain.UserActive {
		return nil
	}
	if err := s.repo.CloseChallenges(ctx, user.ID, domain.PurposePasswordReset); err != nil {
		return err
	}
	token := randomToken()
	if _, err := s.repo.CreateChallenge(ctx, domain.Challenge{UserID: user.ID, Purpose: domain.PurposePasswordReset, TokenHash: hashToken(token), IP: meta.IP, ExpiresAt: s.now().Add(resetLinkTTL)}); err != nil {
		return err
	}
	link := s.cfg.SiteURL + "/auth/set-password?token=" + token
	if err := s.mailer.Send(ctx, resetLinkMail(user, link)); err != nil {
		// Logged, never surfaced: the answer must not depend on the account existing
		s.logger.Error().Err(err).Str("user_id", user.ID).Msg("reset link mail failed")
	}
	return nil
}

// ResetPassword sets a new password from a reset link and signs every browser out
func (s *AuthService) ResetPassword(ctx context.Context, token, password string) error {
	if msg := passwordProblem(password); msg != "" {
		return domain.Validation(map[string]string{"password": msg})
	}
	challenge, err := s.repo.OpenChallenge(ctx, hashToken(token), domain.PurposePasswordReset)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrExpiredLink
	}
	if err != nil {
		return err
	}
	if consumed, err := s.repo.ConsumeChallenge(ctx, challenge.ID); err != nil {
		return err
	} else if !consumed {
		return domain.ErrExpiredLink
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return err
	}
	if err := s.repo.UpdatePassword(ctx, challenge.UserID, hash); err != nil {
		return err
	}
	// The link reached the mailbox, so the email is verified too
	if err := s.repo.MarkEmailVerified(ctx, challenge.UserID); err != nil {
		return err
	}
	return s.repo.RevokeSessions(ctx, challenge.UserID, "")
}

// ChangePassword keeps the current session and signs every other browser out
func (s *AuthService) ChangePassword(ctx context.Context, sessionToken, current, password string) error {
	session, err := s.authenticate(ctx, sessionToken)
	if err != nil {
		return err
	}
	if msg := passwordProblem(password); msg != "" {
		return domain.Validation(map[string]string{"password": msg})
	}
	user, err := s.repo.UserByID(ctx, session.UserID)
	if err != nil {
		return err
	}
	if user.PasswordHash != "" {
		ok, err := s.hasher.Verify(current, user.PasswordHash)
		if err != nil {
			return err
		}
		if !ok {
			return domain.ErrWrongPassword
		}
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return err
	}
	if err := s.repo.UpdatePassword(ctx, user.ID, hash); err != nil {
		return err
	}
	return s.repo.RevokeSessions(ctx, user.ID, session.ID)
}

// --- Sessions and roles ----------------------------------------------------------------------------------------

func (s *AuthService) authenticate(ctx context.Context, token string) (domain.Session, error) {
	if token == "" {
		return domain.Session{}, domain.ErrUnauthenticated
	}
	session, err := s.repo.SessionByToken(ctx, hashToken(token))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Session{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.Session{}, err
	}
	if s.now().Sub(session.LastSeenAt) > sessionTouchEvery {
		if err := s.repo.TouchSession(ctx, session.ID); err != nil {
			s.logger.Warn().Err(err).Msg("session touch failed")
		}
	}
	return session, nil
}

// Me answers who is signed in, with every role and the one in use
func (s *AuthService) Me(ctx context.Context, token string) (domain.Me, error) {
	session, err := s.authenticate(ctx, token)
	if err != nil {
		return domain.Me{}, err
	}
	user, err := s.repo.UserByID(ctx, session.UserID)
	if err != nil {
		return domain.Me{}, err
	}
	if user.Status != domain.UserActive {
		_ = s.repo.RevokeSession(ctx, session.ID)
		return domain.Me{}, domain.ErrUnauthenticated
	}
	roles, err := s.repo.Roles(ctx, user.ID)
	if err != nil {
		return domain.Me{}, err
	}
	me := domain.Me{User: user, Roles: roles, Session: session}
	for i := range roles {
		if session.ActiveUserRoleID != nil && roles[i].ID == *session.ActiveUserRoleID {
			me.ActiveRole = &roles[i]
		}
	}
	if factor, err := s.repo.TOTP(ctx, user.ID); err == nil {
		me.TOTPEnabled = factor.ConfirmedAt != nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Me{}, err
	}
	return me, nil
}

// SetActiveRole switches the role of the current session without signing in again
func (s *AuthService) SetActiveRole(ctx context.Context, token, userRoleID string) error {
	session, err := s.authenticate(ctx, token)
	if err != nil {
		return err
	}
	return s.repo.SetActiveRole(ctx, session.ID, userRoleID)
}

func (s *AuthService) SignOut(ctx context.Context, token string) error {
	session, err := s.authenticate(ctx, token)
	if errors.Is(err, domain.ErrUnauthenticated) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.repo.RevokeSession(ctx, session.ID)
}

// --- Authenticator app (TOTP) ----------------------------------------------------------------------------------

type TOTPSetup struct {
	// Base32, for typing it by hand
	Secret string
	// otpauth:// URI, for the QR code
	URI string
}

// BeginTOTP issues a new secret; it is only asked for at sign-in once ConfirmTOTP accepts a code from it
func (s *AuthService) BeginTOTP(ctx context.Context, token string) (TOTPSetup, error) {
	if !s.secrets.Ready() {
		return TOTPSetup{}, domain.ErrSecretsNotConfigured
	}
	session, err := s.authenticate(ctx, token)
	if err != nil {
		return TOTPSetup{}, err
	}
	user, err := s.repo.UserByID(ctx, session.UserID)
	if err != nil {
		return TOTPSetup{}, err
	}
	secret := make([]byte, totpSecretSize)
	if _, err := rand.Read(secret); err != nil {
		return TOTPSetup{}, err
	}
	ciphertext, err := s.secrets.Encrypt(secret, []byte(user.ID))
	if err != nil {
		return TOTPSetup{}, err
	}
	if err := s.repo.SavePendingTOTP(ctx, user.ID, ciphertext, s.secrets.KeyVersion()); err != nil {
		return TOTPSetup{}, err
	}
	return TOTPSetup{Secret: totpEncoding.EncodeToString(secret), URI: totpURI(totpIssuer, user.Email, secret)}, nil
}

// ConfirmTOTP enables the app after its first code and returns the recovery codes (shown once, stored hashed)
func (s *AuthService) ConfirmTOTP(ctx context.Context, token, code string) ([]string, error) {
	session, err := s.authenticate(ctx, token)
	if err != nil {
		return nil, err
	}
	factor, err := s.repo.TOTP(ctx, session.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, domain.ErrTOTPNotEnrolled
	}
	if err != nil {
		return nil, err
	}
	if factor.ConfirmedAt != nil {
		return nil, domain.ErrTOTPAlreadyEnabled
	}
	secret, err := s.totpSecret(factor)
	if err != nil {
		return nil, err
	}
	step, ok := totpMatch(secret, strings.TrimSpace(code), s.now())
	if !ok {
		return nil, domain.ErrInvalidCode
	}
	codes := make([]string, recoveryCodeCount)
	hashes := make([][]byte, recoveryCodeCount)
	for i := range codes {
		codes[i] = randomRecoveryCode()
		hashes[i] = hashRecoveryCode(codes[i])
	}
	if err := s.repo.ConfirmTOTP(ctx, factor.ID, step, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTOTP turns the app off after proving it once more (app code or recovery code); email codes take over
func (s *AuthService) DisableTOTP(ctx context.Context, token, code string) error {
	session, err := s.authenticate(ctx, token)
	if err != nil {
		return err
	}
	if _, err := s.checkSecondFactor(ctx, session.UserID, strings.TrimSpace(code)); err != nil {
		return err
	}
	return s.repo.DeleteTOTP(ctx, session.UserID)
}

func (s *AuthService) totpSecret(factor domain.TOTPFactor) ([]byte, error) {
	if !s.secrets.Ready() {
		return nil, domain.ErrSecretsNotConfigured
	}
	return s.secrets.Decrypt(factor.SecretCiphertext, []byte(factor.UserID))
}

// --- Seed ------------------------------------------------------------------------------------------------------

// EnsureSuperadmin creates the platform owner from APP__ADMIN__* on start; an existing account keeps its password
func (s *AuthService) EnsureSuperadmin(ctx context.Context, email, password string) error {
	email = normalizeEmail(email)
	if email == "" {
		return nil
	}
	if !validEmail(email) {
		return fmt.Errorf("APP__ADMIN__EMAIL is not a valid email")
	}
	if msg := passwordProblem(password); msg != "" {
		return fmt.Errorf("APP__ADMIN__PASSWORD: %s", msg)
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return err
	}
	created, err := s.repo.EnsureSuperadmin(ctx, email, "Superadmin", hash)
	if err != nil {
		return err
	}
	if created {
		s.logger.Info().Str("email", email).Msg("superadmin account created")
	}
	return nil
}

// --- Helpers ---------------------------------------------------------------------------------------------------

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func validEmail(email string) bool {
	if len(email) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email && strings.Contains(email[strings.LastIndex(email, "@"):], ".")
}

func requireFields(values map[string]string) error {
	fields := map[string]string{}
	if values["email"] == "" {
		fields["email"] = "Ingresa tu correo."
	}
	if values["password"] == "" {
		fields["password"] = "Ingresa tu contraseña."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

// passwordProblem mirrors joseph-app's checklist (features/auth/data/auth-data.ts)
func passwordProblem(password string) string {
	if utf8.RuneCountInString(password) < 10 || len(password) > 128 {
		return "La contraseña debe tener entre 10 y 128 caracteres."
	}
	var upper, lower, digit bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	if !upper || !lower || !digit {
		return "La contraseña debe tener mayúsculas, minúsculas y números."
	}
	return ""
}

// randomToken is 32 random bytes, base64url: session tokens, challenge handles, reset links
func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// hashCode binds the 6 digits to their challenge, so a stored hash is useless for any other sign-in
func hashCode(challengeToken, code string) []byte {
	sum := sha256.Sum256([]byte(challengeToken + ":" + code))
	return sum[:]
}

func randomDigits(n int) string {
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	v, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%0*d", n, v)
}

// Recovery codes read like ABCDE-23456 (no 0/O/1/I to avoid misreading)
const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var recoveryFormat = regexp.MustCompile(`[^A-Z0-9]`)

func randomRecoveryCode() string {
	var b strings.Builder
	for i := 0; i < 10; i++ {
		if i == 5 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(recoveryAlphabet))))
		if err != nil {
			panic(err)
		}
		b.WriteByte(recoveryAlphabet[n.Int64()])
	}
	return b.String()
}

// hashRecoveryCode ignores case, spaces and dashes so the code can be typed loosely
func hashRecoveryCode(code string) []byte {
	sum := sha256.Sum256([]byte(recoveryFormat.ReplaceAllString(strings.ToUpper(code), "")))
	return sum[:]
}
