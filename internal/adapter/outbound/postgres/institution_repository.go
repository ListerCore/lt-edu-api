package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// InstitutionRepository implements port.InstitutionRepository over academic.authorizations and
// academic.platform_sheets
type InstitutionRepository struct {
	c *Client
}

func NewInstitutionRepository(c *Client) *InstitutionRepository {
	return &InstitutionRepository{c: c}
}

var _ port.InstitutionRepository = (*InstitutionRepository)(nil)

func (r *InstitutionRepository) InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error) {
	var i domain.Institution
	err := r.c.pool.QueryRow(ctx, `SELECT id, slug, name, kind FROM iam.institutions WHERE slug = $1 AND is_active`, slug).
		Scan(&i.ID, &i.Slug, &i.Name, &i.Kind)
	return i, notFound(err)
}

// ---------- authorizations ----------

const authorizationColumns = `a.id, a.institution_id, a.kind, coalesce(a.level_id::text, ''), coalesce(l.name, ''), coalesce(a.attention_mode, ''),
	a.resolution, a.issued_by, a.issued_on, a.expires_on, a.file_number, a.notes, a.updated_at,
	(SELECT count(*) FROM attachments.files f WHERE f.institution_id = a.institution_id AND f.owner_kind = 'authorization' AND f.owner_id = a.id)`

const authorizationFrom = ` FROM academic.authorizations a LEFT JOIN academic.levels l ON l.id = a.level_id`

func scanAuthorization(row pgx.Row) (domain.Authorization, error) {
	var a domain.Authorization
	var files int64
	err := row.Scan(&a.ID, &a.InstitutionID, &a.Kind, &a.LevelID, &a.LevelName, &a.AttentionMode, &a.Resolution, &a.IssuedBy,
		&a.IssuedOn, &a.ExpiresOn, &a.FileNumber, &a.Notes, &a.UpdatedAt, &files)
	a.Files = int(files)
	return a, err
}

func (r *InstitutionRepository) Authorizations(ctx context.Context, institutionID string) ([]domain.Authorization, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+authorizationColumns+authorizationFrom+`
		WHERE a.institution_id = $1 ORDER BY a.issued_on DESC, a.created_at DESC`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Authorization, error) { return scanAuthorization(row) })
	if out == nil {
		out = []domain.Authorization{}
	}
	return out, academicErr(err)
}

func (r *InstitutionRepository) Authorization(ctx context.Context, institutionID, id string) (domain.Authorization, error) {
	a, err := scanAuthorization(r.c.pool.QueryRow(ctx, `SELECT `+authorizationColumns+authorizationFrom+`
		WHERE a.institution_id = $1 AND a.id = $2`, institutionID, id))
	return a, academicErr(err)
}

func (r *InstitutionRepository) SaveAuthorization(ctx context.Context, a domain.Authorization) (domain.Authorization, error) {
	var expires *time.Time
	if a.ExpiresOn != nil {
		d := date(*a.ExpiresOn)
		expires = &d
	}
	var id string
	var err error
	// The cycle, when given, must be one of the institution's own
	if a.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO academic.authorizations
			(institution_id, kind, level_id, attention_mode, resolution, issued_by, issued_on, expires_on, file_number, notes)
			SELECT $1, $2, $3::uuid, $4, $5, $6, $7, $8, $9, $10
			WHERE $3::uuid IS NULL OR EXISTS (SELECT 1 FROM academic.levels WHERE id = $3::uuid AND institution_id = $1)
			RETURNING id`,
			a.InstitutionID, a.Kind, nilIfEmpty(a.LevelID), nilIfEmpty(a.AttentionMode), a.Resolution, a.IssuedBy, date(a.IssuedOn), expires, a.FileNumber, a.Notes).Scan(&id)
	} else {
		err = r.c.pool.QueryRow(ctx, `UPDATE academic.authorizations SET kind = $3, level_id = $4::uuid, attention_mode = $5, resolution = $6,
			issued_by = $7, issued_on = $8, expires_on = $9, file_number = $10, notes = $11
			WHERE institution_id = $1 AND id = $2
			  AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM academic.levels WHERE id = $4::uuid AND institution_id = $1))
			RETURNING id`,
			a.InstitutionID, a.ID, a.Kind, nilIfEmpty(a.LevelID), nilIfEmpty(a.AttentionMode), a.Resolution, a.IssuedBy, date(a.IssuedOn), expires, a.FileNumber, a.Notes).Scan(&id)
	}
	if err != nil {
		return domain.Authorization{}, academicErr(err)
	}
	return r.Authorization(ctx, a.InstitutionID, id)
}

// DeleteAuthorization removes the record; the service refuses while it still has files attached
func (r *InstitutionRepository) DeleteAuthorization(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM academic.authorizations WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err != nil {
		return academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *InstitutionRepository) AuthorizedModes(ctx context.Context, institutionID string, day time.Time) (map[string][]string, bool, error) {
	return authorizedModes(ctx, r.c, institutionID, day)
}

// authorizedModes is shared with AcademicRepository, which checks it when sections are created or edited
func authorizedModes(ctx context.Context, c *Client, institutionID string, day time.Time) (map[string][]string, bool, error) {
	rows, err := c.pool.Query(ctx, `SELECT coalesce(level_id::text, ''), attention_mode, issued_on <= $2 AND (expires_on IS NULL OR expires_on >= $2)
		FROM academic.authorizations WHERE institution_id = $1 AND kind = 'attention_mode'`, institutionID, date(day))
	if err != nil {
		return nil, false, academicErr(err)
	}
	defer rows.Close()
	modes := map[string][]string{}
	found := false
	for rows.Next() {
		var level, mode string
		var inForce bool
		if err := rows.Scan(&level, &mode, &inForce); err != nil {
			return nil, false, err
		}
		found = true
		if inForce {
			modes[level] = append(modes[level], mode)
		}
	}
	return modes, found, rows.Err()
}

// ---------- platform sheet ----------

func (r *InstitutionRepository) PlatformSheet(ctx context.Context, institutionID string) (domain.PlatformSheet, error) {
	var p domain.PlatformSheet
	var files int64
	err := r.c.pool.QueryRow(ctx, `SELECT platform_name, url, technology, administrator, access_profiles, hosting_kind, hosting_other, provider,
		instance, contract_reference, service_status, valid_until, technical_lead, technical_lead_specialty, technical_lead_cip,
		updated_by_name, updated_at,
		(SELECT count(*) FROM attachments.files f WHERE f.institution_id = s.institution_id AND f.owner_kind = 'platform_sheet' AND f.owner_id = s.institution_id)
		FROM academic.platform_sheets s WHERE institution_id = $1`, institutionID).
		Scan(&p.PlatformName, &p.URL, &p.Technology, &p.Administrator, &p.AccessProfiles, &p.HostingKind, &p.HostingOther, &p.Provider,
			&p.Instance, &p.ContractReference, &p.ServiceStatus, &p.ValidUntil, &p.TechnicalLead, &p.TechnicalLeadSpecialty, &p.TechnicalLeadCIP,
			&p.UpdatedByName, &p.UpdatedAt, &files)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PlatformSheet{HostingKind: domain.HostingService}, nil
	}
	p.Files = int(files)
	return p, academicErr(err)
}

func (r *InstitutionRepository) SavePlatformSheet(ctx context.Context, institutionID string, p domain.PlatformSheet) (domain.PlatformSheet, error) {
	var valid *time.Time
	if p.ValidUntil != nil {
		d := date(*p.ValidUntil)
		valid = &d
	}
	_, err := r.c.pool.Exec(ctx, `INSERT INTO academic.platform_sheets (institution_id, platform_name, url, technology, administrator, access_profiles,
		hosting_kind, hosting_other, provider, instance, contract_reference, service_status, valid_until, technical_lead,
		technical_lead_specialty, technical_lead_cip, updated_by_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (institution_id) DO UPDATE SET platform_name = EXCLUDED.platform_name, url = EXCLUDED.url, technology = EXCLUDED.technology,
			administrator = EXCLUDED.administrator, access_profiles = EXCLUDED.access_profiles, hosting_kind = EXCLUDED.hosting_kind,
			hosting_other = EXCLUDED.hosting_other, provider = EXCLUDED.provider, instance = EXCLUDED.instance,
			contract_reference = EXCLUDED.contract_reference, service_status = EXCLUDED.service_status, valid_until = EXCLUDED.valid_until,
			technical_lead = EXCLUDED.technical_lead, technical_lead_specialty = EXCLUDED.technical_lead_specialty,
			technical_lead_cip = EXCLUDED.technical_lead_cip, updated_by_name = EXCLUDED.updated_by_name`,
		institutionID, p.PlatformName, p.URL, p.Technology, p.Administrator, p.AccessProfiles, p.HostingKind, p.HostingOther, p.Provider,
		p.Instance, p.ContractReference, p.ServiceStatus, valid, p.TechnicalLead, p.TechnicalLeadSpecialty, p.TechnicalLeadCIP, p.UpdatedByName)
	if err != nil {
		return domain.PlatformSheet{}, academicErr(err)
	}
	return r.PlatformSheet(ctx, institutionID)
}

// ---------- members ----------

// memberQuery reads account roles and pending invitations of the institution; $2 filters by role codes (NULL: all)
const memberQuery = `SELECT * FROM (
	SELECT ur.id::text AS id, false AS pending, u.id::text AS user_id, u.email, u.full_name, r.code, ur.granted_at AS since, u.last_sign_in_at
	FROM iam.user_roles ur JOIN iam.users u ON u.id = ur.user_id JOIN iam.roles r ON r.id = ur.role_id
	WHERE ur.institution_id = $1 AND ($2::text[] IS NULL OR r.code = ANY($2))
	UNION ALL
	SELECT i.id::text, true, '', i.email, '', r.code, i.created_at, NULL::timestamptz
	FROM iam.invitations i JOIN iam.roles r ON r.id = i.role_id
	WHERE i.institution_id = $1 AND ($2::text[] IS NULL OR r.code = ANY($2)) AND i.accepted_at IS NULL AND i.revoked_at IS NULL
) m`

func scanMember(row pgx.Row) (domain.Member, error) {
	var m domain.Member
	err := row.Scan(&m.ID, &m.Pending, &m.UserID, &m.Email, &m.FullName, &m.RoleCode, &m.Since, &m.LastSignIn)
	return m, err
}

func (r *InstitutionRepository) Members(ctx context.Context, institutionID string, roles []string) ([]domain.Member, error) {
	rows, err := r.c.pool.Query(ctx, memberQuery+` ORDER BY pending, lower(full_name), email, code`, institutionID, roles)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Member, error) { return scanMember(row) })
	if out == nil {
		out = []domain.Member{}
	}
	return out, academicErr(err)
}

func (r *InstitutionRepository) Member(ctx context.Context, institutionID, id string) (domain.Member, error) {
	// No role filter: any access of the institution
	m, err := scanMember(r.c.pool.QueryRow(ctx, memberQuery+` WHERE m.id = $3`, institutionID, nil, id))
	return m, academicErr(err)
}

func (r *InstitutionRepository) GrantRole(ctx context.Context, institutionID, email, roleCode, grantedBy string) (bool, error) {
	pending := false
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		var userID *string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM iam.users WHERE lower(email) = $1`, email).Scan(&userID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var tag interface{ RowsAffected() int64 }
		var err error
		if userID != nil {
			tag, err = tx.Exec(ctx, `INSERT INTO iam.user_roles (user_id, role_id, role_scope, institution_id, granted_by)
				SELECT $1, r.id, 'institution', $2, $4::uuid FROM iam.roles r WHERE r.code = $3 AND r.scope = 'institution'
				ON CONFLICT DO NOTHING`, *userID, institutionID, roleCode, nilIfEmpty(grantedBy))
		} else {
			pending = true
			tag, err = tx.Exec(ctx, `INSERT INTO iam.invitations (institution_id, email, role_id, invited_by)
				SELECT $1, $2, r.id, $4::uuid FROM iam.roles r WHERE r.code = $3 AND r.scope = 'institution'
				ON CONFLICT DO NOTHING`, institutionID, email, roleCode, nilIfEmpty(grantedBy))
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrMemberExists
		}
		return nil
	})
	if errors.Is(err, domain.ErrMemberExists) {
		return false, err
	}
	return pending, academicErr(err)
}

func (r *InstitutionRepository) CountRole(ctx context.Context, institutionID, roleCode string) (int, error) {
	var n int
	err := r.c.pool.QueryRow(ctx, `SELECT count(*) FROM iam.user_roles ur JOIN iam.roles r ON r.id = ur.role_id
		WHERE ur.institution_id = $1 AND r.code = $2`, institutionID, roleCode).Scan(&n)
	return n, err
}

func (r *InstitutionRepository) RevokeMember(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM iam.user_roles WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err != nil {
		return academicErr(err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	tag, err = r.c.pool.Exec(ctx, `UPDATE iam.invitations SET revoked_at = now()
		WHERE institution_id = $1 AND id = $2 AND accepted_at IS NULL AND revoked_at IS NULL`, institutionID, id)
	if err != nil {
		return academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *InstitutionRepository) DisabledModules(ctx context.Context, institutionID string) ([]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT module FROM iam.disabled_modules WHERE institution_id = $1 ORDER BY module`, institutionID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (r *InstitutionRepository) SetModuleDisabled(ctx context.Context, institutionID, module string, disabled bool, userID string) error {
	if !disabled {
		_, err := r.c.pool.Exec(ctx, `DELETE FROM iam.disabled_modules WHERE institution_id = $1 AND module = $2`, institutionID, module)
		return err
	}
	_, err := r.c.pool.Exec(ctx, `INSERT INTO iam.disabled_modules (institution_id, module, disabled_by) VALUES ($1, $2, nullif($3, '')::uuid)
		ON CONFLICT (institution_id, module) DO NOTHING`, institutionID, module, userID)
	return err
}
