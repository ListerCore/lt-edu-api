package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// NewSession is what the repository stores when a sign-in completes
type NewSession struct {
	UserID           string
	TokenHash        []byte
	ActiveUserRoleID *string
	MFAMethod        string
	IP               string
	UserAgent        string
	ExpiresAt        time.Time
}

// AuthRepository is iam.* in PostgreSQL. Lookups that find nothing return domain.ErrNotFound
type AuthRepository interface {
	UserByEmail(ctx context.Context, email string) (domain.User, error)
	UserByID(ctx context.Context, id string) (domain.User, error)
	// RecordFailedSignIn counts a wrong password and locks the account for lockFor once max is reached
	RecordFailedSignIn(ctx context.Context, userID string, max int, lockFor time.Duration) error
	// CreateUserFromInvitations creates the account and turns every pending invitation of the email into a role,
	// in one transaction. domain.ErrNotInvited when there is none, domain.ErrEmailTaken when the email has an account.
	// passwordHash may be empty (Google account); with identity the provider is linked and the email marked verified
	CreateUserFromInvitations(ctx context.Context, email, fullName, passwordHash string, identity *ExternalIdentity) (string, error)
	// UserByIdentity finds the account linked to a provider subject
	UserByIdentity(ctx context.Context, provider, subject string) (domain.User, error)
	// LinkIdentity links (or refreshes) a provider account and marks the email verified
	LinkIdentity(ctx context.Context, userID string, identity ExternalIdentity) error
	// EnsureSuperadmin creates the account if missing and grants it the superadmin role; reports whether it created it
	EnsureSuperadmin(ctx context.Context, email, fullName, passwordHash string) (bool, error)
	UpdatePassword(ctx context.Context, userID, passwordHash string) error
	MarkEmailVerified(ctx context.Context, userID string) error

	Roles(ctx context.Context, userID string) ([]domain.RoleAssignment, error)

	CreateChallenge(ctx context.Context, c domain.Challenge) (string, error)
	// OpenChallenge finds a challenge that is neither consumed nor expired
	OpenChallenge(ctx context.Context, tokenHash []byte, purpose string) (domain.Challenge, error)
	// CountAttempt adds one attempt and returns the new total
	CountAttempt(ctx context.Context, challengeID string) (int, error)
	ReplaceCode(ctx context.Context, challengeID string, codeHash []byte) error
	// ConsumeChallenge marks it used; false when another request consumed it first
	ConsumeChallenge(ctx context.Context, challengeID string) (bool, error)
	// CloseChallenges consumes every open challenge of the user for purpose (a new one replaces them)
	CloseChallenges(ctx context.Context, userID, purpose string) error

	// CompleteSignIn stores the session, clears the lockout counters and stamps last_sign_in_at
	CompleteSignIn(ctx context.Context, s NewSession) (string, error)
	SessionByToken(ctx context.Context, tokenHash []byte) (domain.Session, error)
	TouchSession(ctx context.Context, sessionID string) error
	SetActiveRole(ctx context.Context, sessionID, userRoleID string) error
	RevokeSession(ctx context.Context, sessionID string) error
	// RevokeSessions ends every live session of the user except keepID (empty keeps none)
	RevokeSessions(ctx context.Context, userID, keepID string) error

	TOTP(ctx context.Context, userID string) (domain.TOTPFactor, error)
	// SavePendingTOTP replaces an unconfirmed factor with a new secret
	SavePendingTOTP(ctx context.Context, userID string, ciphertext []byte, keyVersion int) error
	// ConfirmTOTP enables the factor, records the step used and replaces the recovery codes
	ConfirmTOTP(ctx context.Context, factorID string, step int64, recoveryHashes [][]byte) error
	// UseTOTPStep records step only if it is newer than the last one used; false means a replay
	UseTOTPStep(ctx context.Context, factorID string, step int64) (bool, error)
	DeleteTOTP(ctx context.Context, userID string) error
	// UseRecoveryCode spends a matching unused code and keeps it (display) to show it back; false when none matches
	UseRecoveryCode(ctx context.Context, userID string, codeHash []byte, display string) (bool, error)
	ReplaceRecoveryCodes(ctx context.Context, userID string, hashes [][]byte) error
	// RecoveryCodes lists the current codes, unused first
	RecoveryCodes(ctx context.Context, userID string) ([]domain.RecoveryCode, error)
	// LatestOpenChallenge is the newest open challenge of the user for purpose
	LatestOpenChallenge(ctx context.Context, userID, purpose string) (domain.Challenge, error)
	HasIdentity(ctx context.Context, userID, provider string) (bool, error)
}

// ExternalIdentity is an account at a sign-in provider whose email the provider verified
type ExternalIdentity struct {
	Provider string
	Subject  string
	Email    string
	FullName string
}

// GoogleOAuth is Google's OAuth 2.0 / OpenID Connect with PKCE
type GoogleOAuth interface {
	Configured() bool
	AuthorizationURL(state, codeChallenge string) string
	// Exchange trades the authorization code for the verified identity
	Exchange(ctx context.Context, code, codeVerifier string) (ExternalIdentity, error)
}

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (bool, error)
}

// CaptchaVerifier checks a Cloudflare Turnstile token against the caller's IP
type CaptchaVerifier interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// SecretBox encrypts secrets at rest (TOTP seeds)
type SecretBox interface {
	Ready() bool
	KeyVersion() int
	Encrypt(plaintext, associatedData []byte) ([]byte, error)
	Decrypt(ciphertext, associatedData []byte) ([]byte, error)
}

type Mail struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type Mailer interface {
	Send(ctx context.Context, mail Mail) error
}
