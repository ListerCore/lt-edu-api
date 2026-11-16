package postgres

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// AuditRepository implements port.AuditRepository over audit.events
type AuditRepository struct {
	c *Client
}

func NewAuditRepository(c *Client) *AuditRepository {
	return &AuditRepository{c: c}
}

var _ port.AuditRepository = (*AuditRepository)(nil)

func (r *AuditRepository) InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error) {
	var i domain.Institution
	err := r.c.pool.QueryRow(ctx, `SELECT id, slug, name, kind FROM iam.institutions WHERE slug = $1 AND is_active`, slug).
		Scan(&i.ID, &i.Slug, &i.Name, &i.Kind)
	return i, notFound(err)
}

func (r *AuditRepository) Record(ctx context.Context, e domain.AuditEvent) error {
	changes := []byte("{}")
	if len(e.Changes) > 0 {
		b, err := json.Marshal(e.Changes)
		if err != nil {
			return err
		}
		changes = b
	}
	_, err := r.c.pool.Exec(ctx, `INSERT INTO audit.events (institution_id, user_id, user_name, role, module, action, entity, entity_id, summary, changes)
		VALUES ($1::uuid, $2::uuid, coalesce(nullif($3, ''), (SELECT full_name FROM iam.users WHERE id = $2::uuid), ''), $4, $5, $6, $7, $8, $9, $10)`,
		nilIfEmpty(e.InstitutionID), nilIfEmpty(e.UserID), e.UserName, e.Role, e.Module, e.Action, e.Entity, e.EntityID, e.Summary, changes)
	return err
}

func (r *AuditRepository) Events(ctx context.Context, institutionID string, f domain.AuditFilter) ([]domain.AuditEvent, error) {
	args := []any{institutionID}
	where := `institution_id = $1`
	add := func(cond string, v any) {
		args = append(args, v)
		where += " AND " + cond + " $" + strconv.Itoa(len(args))
	}
	if f.Module != "" {
		add("module =", f.Module)
	}
	if f.UserID != "" {
		add("user_id =", f.UserID)
	}
	if f.From != nil {
		add("created_at >=", *f.From)
	}
	if f.To != nil {
		add("created_at <", *f.To)
	}
	if f.BeforeAt != nil {
		args = append(args, *f.BeforeAt, f.BeforeID)
		n := len(args)
		where += " AND (created_at, id) < ($" + strconv.Itoa(n-1) + ", $" + strconv.Itoa(n) + "::uuid)"
	}
	args = append(args, f.Limit)
	rows, err := r.c.pool.Query(ctx, `SELECT id, coalesce(institution_id::text, ''), coalesce(user_id::text, ''), user_name, role, module, action,
		entity, entity_id, summary, changes, created_at FROM audit.events WHERE `+where+`
		ORDER BY created_at DESC, id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.AuditEvent, error) {
		var e domain.AuditEvent
		var changes []byte
		if err := row.Scan(&e.ID, &e.InstitutionID, &e.UserID, &e.UserName, &e.Role, &e.Module, &e.Action, &e.Entity, &e.EntityID,
			&e.Summary, &changes, &e.CreatedAt); err != nil {
			return e, err
		}
		if len(changes) > 2 {
			_ = json.Unmarshal(changes, &e.Changes)
		}
		return e, nil
	})
	if out == nil {
		out = []domain.AuditEvent{}
	}
	return out, academicErr(err)
}
