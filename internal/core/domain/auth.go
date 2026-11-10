package domain

import "time"

// Account states (iam.users.status)
const (
	UserActive    = "active"
	UserSuspended = "suspended"
)

// Role scopes (iam.roles.scope)
const (
	ScopePlatform    = "platform"
	ScopeInstitution = "institution"
)

const RoleSuperadmin = "superadmin"

// Second-step methods
const (
	MFAEmail        = "email"
	MFATOTP         = "totp"
	MFARecoveryCode = "recovery_code"
	MFAGoogle       = "google"
)

// External sign-in providers (iam.user_identities.provider)
const ProviderGoogle = "google"

// Challenge purposes (iam.verification_challenges.purpose)
const (
	PurposeSignIn        = "sign_in"
	PurposePasswordReset = "password_reset"
)

type User struct {
	ID              string
	Email           string
	FullName        string
	AvatarURL       *string
	Status          string
	EmailVerifiedAt *time.Time
	// Empty when the account has no password yet
	PasswordHash   string
	FailedAttempts int
	LockedUntil    *time.Time
}

type Institution struct {
	ID   string
	Slug string
	Name string
	Kind string
}

// RoleAssignment is one iam.user_roles row: a role, and for institution roles the institution it applies to
type RoleAssignment struct {
	ID          string
	RoleCode    string
	RoleName    string
	Scope       string
	Institution *Institution
}

type Session struct {
	ID               string
	UserID           string
	ActiveUserRoleID *string
	MFAMethod        string
	LastSeenAt       time.Time
	ExpiresAt        time.Time
}

// Me is the signed-in account as the app shows it
type Me struct {
	User        User
	Roles       []RoleAssignment
	ActiveRole  *RoleAssignment
	TOTPEnabled bool
	Session     Session
}

type Challenge struct {
	ID         string
	UserID     string
	Purpose    string
	TokenHash  []byte
	Method     string
	CodeHash   []byte
	CodeSentAt *time.Time
	Attempts   int
	IP         string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

type TOTPFactor struct {
	ID               string
	UserID           string
	SecretCiphertext []byte
	KeyVersion       int
	ConfirmedAt      *time.Time
	LastUsedStep     *int64
}
