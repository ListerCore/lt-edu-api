package postgres

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"api/internal/core/domain"
	"api/internal/core/port"
)

const uniqueViolation = "23505"

// AuthRepository implements port.AuthRepository over the iam schema
type AuthRepository struct {
	c *Client
}

func NewAuthRepository(c *Client) *AuthRepository {
	return &AuthRepository{c: c}
}

var _ port.AuthRepository = (*AuthRepository)(nil)

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// inet columns take NULL when the IP is unknown or malformed
func inet(ip string) *string {
	if _, err := netip.ParseAddr(ip); err != nil {
		return nil
	}
	return &ip
}

const userColumns = `u.id, u.email, u.full_name, u.avatar_url, u.status, u.email_verified_at, coalesce(u.password_hash, ''),
	u.failed_sign_in_attempts, u.locked_until, u.password_changed_at, u.last_sign_in_at, u.created_at`

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.AvatarURL, &u.Status, &u.EmailVerifiedAt, &u.PasswordHash,
		&u.FailedAttempts, &u.LockedUntil, &u.PasswordChangedAt, &u.LastSignInAt, &u.CreatedAt)
	return u, notFound(err)
}

func (r *AuthRepository) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	return scanUser(r.c.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM iam.users u WHERE u.email = $1`, email))
}

func (r *AuthRepository) UserByID(ctx context.Context, id string) (domain.User, error) {
	return scanUser(r.c.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM iam.users u WHERE u.id = $1`, id))
}

func (r *AuthRepository) RecordFailedSignIn(ctx context.Context, userID string, max int, lockFor time.Duration) error {
	// The counter restarts once a lock has run out, so an old streak never locks the next attempt
	_, err := r.c.pool.Exec(ctx, `
		UPDATE iam.users SET
			failed_sign_in_attempts = CASE WHEN locked_until IS NOT NULL AND locked_until <= now() THEN 1
				ELSE failed_sign_in_attempts + 1 END,
			locked_until = CASE
				WHEN locked_until IS NOT NULL AND locked_until <= now() THEN NULL
				WHEN failed_sign_in_attempts + 1 >= $2 THEN now() + $3::interval
				ELSE locked_until END
		WHERE id = $1`, userID, max, lockFor.String())
	return err
}

func (r *AuthRepository) CreateUserFromInvitations(ctx context.Context, email, fullName, passwordHash string, identity *port.ExternalIdentity) (string, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the pending invitations so two sign-ups of the same email cannot both accept them
	rows, err := tx.Query(ctx, `
		SELECT id, institution_id, role_id, role_scope, invited_by FROM iam.invitations
		WHERE email = $1 AND accepted_at IS NULL AND revoked_at IS NULL
		FOR UPDATE`, email)
	if err != nil {
		return "", err
	}
	type invitation struct {
		id, institutionID string
		roleID            int16
		roleScope         string
		invitedBy         *string
	}
	var invitations []invitation
	for rows.Next() {
		var inv invitation
		if err := rows.Scan(&inv.id, &inv.institutionID, &inv.roleID, &inv.roleScope, &inv.invitedBy); err != nil {
			rows.Close()
			return "", err
		}
		invitations = append(invitations, inv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(invitations) == 0 {
		// Same answer whether or not the email already has an account would leak less, but the form needs to
		// tell a registered person to sign in instead
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM iam.users WHERE email = $1)`, email).Scan(&exists); err != nil {
			return "", err
		}
		if exists {
			return "", domain.ErrEmailTaken
		}
		return "", domain.ErrNotInvited
	}

	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO iam.users (email, full_name, password_hash, password_changed_at, email_verified_at)
		VALUES ($1, $2, nullif($3, ''), CASE WHEN $3 <> '' THEN now() END, CASE WHEN $4 THEN now() END)
		RETURNING id`, email, fullName, passwordHash, identity != nil).Scan(&userID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return "", domain.ErrEmailTaken
	}
	if err != nil {
		return "", err
	}

	for _, inv := range invitations {
		if _, err := tx.Exec(ctx, `
			INSERT INTO iam.user_roles (user_id, role_id, role_scope, institution_id, granted_by)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`,
			userID, inv.roleID, inv.roleScope, inv.institutionID, inv.invitedBy); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE iam.invitations SET accepted_at = now(), accepted_by = $2 WHERE id = $1`, inv.id, userID); err != nil {
			return "", err
		}
	}
	if identity != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO iam.user_identities (user_id, provider, subject, email) VALUES ($1, $2, $3, $4)`,
			userID, identity.Provider, identity.Subject, identity.Email); err != nil {
			return "", err
		}
	}
	return userID, tx.Commit(ctx)
}

func (r *AuthRepository) UserByIdentity(ctx context.Context, provider, subject string) (domain.User, error) {
	return scanUser(r.c.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM iam.users u
		JOIN iam.user_identities i ON i.user_id = u.id WHERE i.provider = $1 AND i.subject = $2`, provider, subject))
}

func (r *AuthRepository) LinkIdentity(ctx context.Context, userID string, identity port.ExternalIdentity) error {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A provider account linked to someone else stays with them: the conflict leaves 0 rows and is reported
	tag, err := tx.Exec(ctx, `
		INSERT INTO iam.user_identities (user_id, provider, subject, email) VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, subject) DO UPDATE SET email = EXCLUDED.email, last_used_at = now()
		WHERE iam.user_identities.user_id = EXCLUDED.user_id`,
		userID, identity.Provider, identity.Subject, identity.Email)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		// The account already links another Google account
		return domain.ErrGoogleFailed
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrGoogleFailed
	}
	if _, err := tx.Exec(ctx, `UPDATE iam.users SET email_verified_at = now() WHERE id = $1 AND email_verified_at IS NULL`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *AuthRepository) EnsureSuperadmin(ctx context.Context, email, fullName, passwordHash string) (bool, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// An existing account keeps its password: the seed only guarantees the role
	var userID string
	var created bool
	err = tx.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO iam.users (email, full_name, password_hash, password_changed_at, email_verified_at)
			VALUES ($1, $2, $3, now(), now())
			ON CONFLICT (email) DO NOTHING
			RETURNING id
		)
		SELECT id, true FROM inserted
		UNION ALL
		SELECT id, false FROM iam.users WHERE email = $1
		LIMIT 1`, email, fullName, passwordHash).Scan(&userID, &created)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO iam.user_roles (user_id, role_id, role_scope)
		SELECT $1, id, scope FROM iam.roles WHERE code = $2
		ON CONFLICT DO NOTHING`, userID, domain.RoleSuperadmin); err != nil {
		return false, err
	}
	return created, tx.Commit(ctx)
}

func (r *AuthRepository) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	_, err := r.c.pool.Exec(ctx, `
		UPDATE iam.users SET password_hash = $2, password_changed_at = now(), failed_sign_in_attempts = 0, locked_until = NULL
		WHERE id = $1`, userID, passwordHash)
	return err
}

func (r *AuthRepository) MarkEmailVerified(ctx context.Context, userID string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE iam.users SET email_verified_at = now() WHERE id = $1 AND email_verified_at IS NULL`, userID)
	return err
}

func (r *AuthRepository) Roles(ctx context.Context, userID string) ([]domain.RoleAssignment, error) {
	// Roles of inactive institutions disappear for everyone
	rows, err := r.c.pool.Query(ctx, `
		SELECT ur.id, ro.code, ro.name, ro.scope, i.id, i.slug, i.name, i.kind
		FROM iam.user_roles ur
		JOIN iam.roles ro ON ro.id = ur.role_id
		LEFT JOIN iam.institutions i ON i.id = ur.institution_id
		WHERE ur.user_id = $1 AND (ur.institution_id IS NULL OR i.is_active)
		ORDER BY ro.scope = 'institution', i.name NULLS FIRST, ro.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.RoleAssignment
	for rows.Next() {
		var a domain.RoleAssignment
		var instID, instSlug, instName, instKind *string
		if err := rows.Scan(&a.ID, &a.RoleCode, &a.RoleName, &a.Scope, &instID, &instSlug, &instName, &instKind); err != nil {
			return nil, err
		}
		if instID != nil {
			a.Institution = &domain.Institution{ID: *instID, Slug: *instSlug, Name: *instName, Kind: *instKind}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const challengeColumns = `id, user_id, purpose, token_hash, coalesce(method, ''), code_hash, code_sent_at, attempts,
	coalesce(host(ip), ''), created_at, expires_at, consumed_at`

func (r *AuthRepository) CreateChallenge(ctx context.Context, c domain.Challenge) (string, error) {
	var method *string
	if c.Method != "" {
		method = &c.Method
	}
	var id string
	err := r.c.pool.QueryRow(ctx, `
		INSERT INTO iam.verification_challenges (user_id, purpose, token_hash, method, code_hash, code_sent_at, ip, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		c.UserID, c.Purpose, c.TokenHash, method, c.CodeHash, c.CodeSentAt, inet(c.IP), c.ExpiresAt).Scan(&id)
	return id, err
}

func (r *AuthRepository) OpenChallenge(ctx context.Context, tokenHash []byte, purpose string) (domain.Challenge, error) {
	var c domain.Challenge
	err := r.c.pool.QueryRow(ctx, `SELECT `+challengeColumns+` FROM iam.verification_challenges
		WHERE token_hash = $1 AND purpose = $2 AND consumed_at IS NULL AND expires_at > now()`, tokenHash, purpose).
		Scan(&c.ID, &c.UserID, &c.Purpose, &c.TokenHash, &c.Method, &c.CodeHash, &c.CodeSentAt, &c.Attempts, &c.IP,
			&c.CreatedAt, &c.ExpiresAt, &c.ConsumedAt)
	return c, notFound(err)
}

func (r *AuthRepository) CountAttempt(ctx context.Context, challengeID string) (int, error) {
	var attempts int
	err := r.c.pool.QueryRow(ctx, `UPDATE iam.verification_challenges SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts`, challengeID).Scan(&attempts)
	return attempts, notFound(err)
}

func (r *AuthRepository) ReplaceCode(ctx context.Context, challengeID string, codeHash []byte) error {
	// A new code also restarts the attempt budget
	_, err := r.c.pool.Exec(ctx, `UPDATE iam.verification_challenges SET code_hash = $2, code_sent_at = now(), attempts = 0 WHERE id = $1`, challengeID, codeHash)
	return err
}

func (r *AuthRepository) ConsumeChallenge(ctx context.Context, challengeID string) (bool, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE iam.verification_challenges SET consumed_at = now() WHERE id = $1 AND consumed_at IS NULL`, challengeID)
	return tag.RowsAffected() == 1, err
}

func (r *AuthRepository) CloseChallenges(ctx context.Context, userID, purpose string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE iam.verification_challenges SET consumed_at = now()
		WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL`, userID, purpose)
	return err
}

func (r *AuthRepository) CompleteSignIn(ctx context.Context, s port.NewSession) (string, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO iam.sessions (user_id, token_hash, active_user_role_id, mfa_method, ip, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, left($6, 512), $7) RETURNING id`,
		s.UserID, s.TokenHash, s.ActiveUserRoleID, s.MFAMethod, inet(s.IP), s.UserAgent, s.ExpiresAt).Scan(&id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE iam.users SET failed_sign_in_attempts = 0, locked_until = NULL, last_sign_in_at = now() WHERE id = $1`, s.UserID); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (r *AuthRepository) SessionByToken(ctx context.Context, tokenHash []byte) (domain.Session, error) {
	var s domain.Session
	err := r.c.pool.QueryRow(ctx, `
		SELECT id, user_id, active_user_role_id, mfa_method, last_seen_at, expires_at FROM iam.sessions
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`, tokenHash).
		Scan(&s.ID, &s.UserID, &s.ActiveUserRoleID, &s.MFAMethod, &s.LastSeenAt, &s.ExpiresAt)
	return s, notFound(err)
}

func (r *AuthRepository) TouchSession(ctx context.Context, sessionID string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE iam.sessions SET last_seen_at = now() WHERE id = $1`, sessionID)
	return err
}

func (r *AuthRepository) SetActiveRole(ctx context.Context, sessionID, userRoleID string) error {
	// The composite foreign key refuses an assignment of another user
	tag, err := r.c.pool.Exec(ctx, `
		UPDATE iam.sessions s SET active_user_role_id = ur.id
		FROM iam.user_roles ur
		WHERE s.id = $1 AND ur.id = $2 AND ur.user_id = s.user_id`, sessionID, userRoleID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrRoleNotAssigned
	}
	return nil
}

func (r *AuthRepository) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE iam.sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, sessionID)
	return err
}

func (r *AuthRepository) RevokeSessions(ctx context.Context, userID, keepID string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE iam.sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL AND ($2 = '' OR id::text <> $2)`, userID, keepID)
	return err
}

func (r *AuthRepository) TOTP(ctx context.Context, userID string) (domain.TOTPFactor, error) {
	var f domain.TOTPFactor
	err := r.c.pool.QueryRow(ctx, `
		SELECT id, user_id, secret_ciphertext, key_version, confirmed_at, last_used_step FROM iam.mfa_factors
		WHERE user_id = $1 AND kind = 'totp'`, userID).
		Scan(&f.ID, &f.UserID, &f.SecretCiphertext, &f.KeyVersion, &f.ConfirmedAt, &f.LastUsedStep)
	return f, notFound(err)
}

func (r *AuthRepository) SavePendingTOTP(ctx context.Context, userID string, ciphertext []byte, keyVersion int) error {
	tag, err := r.c.pool.Exec(ctx, `
		INSERT INTO iam.mfa_factors (user_id, kind, secret_ciphertext, key_version) VALUES ($1, 'totp', $2, $3)
		ON CONFLICT (user_id, kind) DO UPDATE SET secret_ciphertext = EXCLUDED.secret_ciphertext,
			key_version = EXCLUDED.key_version, last_used_step = NULL
		WHERE iam.mfa_factors.confirmed_at IS NULL`, userID, ciphertext, keyVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTOTPAlreadyEnabled
	}
	return nil
}

func (r *AuthRepository) ConfirmTOTP(ctx context.Context, factorID string, step int64, recoveryHashes [][]byte) error {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID string
	if err := tx.QueryRow(ctx, `
		UPDATE iam.mfa_factors SET confirmed_at = now(), last_used_step = $2
		WHERE id = $1 AND confirmed_at IS NULL RETURNING user_id`, factorID, step).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrTOTPAlreadyEnabled
		}
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM iam.mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, hash := range recoveryHashes {
		if _, err := tx.Exec(ctx, `INSERT INTO iam.mfa_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, hash); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *AuthRepository) UseTOTPStep(ctx context.Context, factorID string, step int64) (bool, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE iam.mfa_factors SET last_used_step = $2
		WHERE id = $1 AND (last_used_step IS NULL OR last_used_step < $2)`, factorID, step)
	return tag.RowsAffected() == 1, err
}

func (r *AuthRepository) DeleteTOTP(ctx context.Context, userID string) error {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM iam.mfa_factors WHERE user_id = $1 AND kind = 'totp'`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM iam.mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *AuthRepository) UseRecoveryCode(ctx context.Context, userID string, codeHash []byte, display string) (bool, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE iam.mfa_recovery_codes SET used_at = now(), used_code = $3
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, codeHash, display)
	return tag.RowsAffected() == 1, err
}

func (r *AuthRepository) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes [][]byte) error {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM iam.mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, hash := range hashes {
		if _, err := tx.Exec(ctx, `INSERT INTO iam.mfa_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, hash); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *AuthRepository) RecoveryCodes(ctx context.Context, userID string) ([]domain.RecoveryCode, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT used_code, used_at FROM iam.mfa_recovery_codes
		WHERE user_id = $1 ORDER BY used_at NULLS FIRST, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RecoveryCode
	for rows.Next() {
		var c domain.RecoveryCode
		if err := rows.Scan(&c.UsedCode, &c.UsedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *AuthRepository) LatestOpenChallenge(ctx context.Context, userID, purpose string) (domain.Challenge, error) {
	var c domain.Challenge
	err := r.c.pool.QueryRow(ctx, `SELECT `+challengeColumns+` FROM iam.verification_challenges
		WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC LIMIT 1`, userID, purpose).
		Scan(&c.ID, &c.UserID, &c.Purpose, &c.TokenHash, &c.Method, &c.CodeHash, &c.CodeSentAt, &c.Attempts, &c.IP,
			&c.CreatedAt, &c.ExpiresAt, &c.ConsumedAt)
	return c, notFound(err)
}

func (r *AuthRepository) HasIdentity(ctx context.Context, userID, provider string) (bool, error) {
	var exists bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM iam.user_identities WHERE user_id = $1 AND provider = $2)`, userID, provider).Scan(&exists)
	return exists, err
}
