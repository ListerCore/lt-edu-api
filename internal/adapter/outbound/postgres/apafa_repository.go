package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ApafaRepository implements port.ApafaRepository over the apafa schema
type ApafaRepository struct {
	c *Client
}

func NewApafaRepository(c *Client) *ApafaRepository {
	return &ApafaRepository{c: c}
}

var _ port.ApafaRepository = (*ApafaRepository)(nil)

func (r *ApafaRepository) InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error) {
	var i domain.Institution
	err := r.c.pool.QueryRow(ctx, `SELECT id, slug, name, kind FROM iam.institutions WHERE slug = $1 AND is_active`, slug).
		Scan(&i.ID, &i.Slug, &i.Name, &i.Kind)
	return i, notFound(err)
}

// Management reuses finance.management_of (migration 0022): private when the institution has no profile yet
func (r *ApafaRepository) Management(ctx context.Context, institutionID string) (string, error) {
	var m string
	err := r.c.pool.QueryRow(ctx, `SELECT finance.management_of($1)`, institutionID).Scan(&m)
	return m, err
}

const meetingColumns = `m.id, m.institution_id, m.title, m.kind, m.scheduled_at, m.modality, m.place, m.meeting_url, m.agenda, m.audience,
	m.status, m.previous_at, m.reschedule_reason, m.cancel_reason, m.minutes, m.agreements, m.attendees, m.minutes_published_at,
	coalesce(m.announcement_id::text, ''), m.notified_at, coalesce(m.created_by::text, ''), m.created_name, m.created_at, m.updated_at,
	(SELECT count(*) FROM attachments.files f WHERE f.owner_kind = 'apafa_meeting' AND f.owner_id = m.id)::int`

func scanMeeting(row pgx.Row) (domain.ApafaMeeting, error) {
	var m domain.ApafaMeeting
	err := row.Scan(&m.ID, &m.InstitutionID, &m.Title, &m.Kind, &m.ScheduledAt, &m.Modality, &m.Place, &m.MeetingURL, &m.Agenda, &m.Audience,
		&m.Status, &m.PreviousAt, &m.RescheduleReason, &m.CancelReason, &m.Minutes, &m.Agreements, &m.Attendees, &m.MinutesAt,
		&m.AnnouncementID, &m.NotifiedAt, &m.CreatedBy, &m.CreatedName, &m.CreatedAt, &m.UpdatedAt, &m.Files)
	return m, err
}

func (r *ApafaRepository) Meetings(ctx context.Context, institutionID string, drafts bool) ([]domain.ApafaMeeting, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+meetingColumns+` FROM apafa.meetings m
		WHERE m.institution_id = $1 AND ($2 OR m.notified_at IS NOT NULL) ORDER BY m.scheduled_at DESC, m.id DESC`, institutionID, drafts)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ApafaMeeting, error) { return scanMeeting(row) })
	if out == nil {
		out = []domain.ApafaMeeting{}
	}
	return out, err
}

func (r *ApafaRepository) Meeting(ctx context.Context, institutionID, id string) (domain.ApafaMeeting, error) {
	m, err := scanMeeting(r.c.pool.QueryRow(ctx, `SELECT `+meetingColumns+` FROM apafa.meetings m WHERE m.institution_id = $1 AND m.id = $2`, institutionID, id))
	return m, academicErr(err)
}

func (r *ApafaRepository) CreateMeeting(ctx context.Context, m domain.ApafaMeeting) (domain.ApafaMeeting, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO apafa.meetings
		(institution_id, title, kind, scheduled_at, modality, place, meeting_url, agenda, audience, created_by, created_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		m.InstitutionID, m.Title, m.Kind, m.ScheduledAt, m.Modality, m.Place, m.MeetingURL, m.Agenda, m.Audience, optionalText(m.CreatedBy), m.CreatedName).Scan(&id)
	if err != nil {
		return domain.ApafaMeeting{}, academicErr(err)
	}
	return r.Meeting(ctx, m.InstitutionID, id)
}

func (r *ApafaRepository) UpdateMeeting(ctx context.Context, m domain.ApafaMeeting) (domain.ApafaMeeting, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE apafa.meetings SET title = $3, kind = $4, scheduled_at = $5, modality = $6, place = $7, meeting_url = $8,
		agenda = $9, audience = $10, status = $11, previous_at = $12, reschedule_reason = $13, cancel_reason = $14, minutes = $15, agreements = $16,
		attendees = $17, minutes_published_at = $18, announcement_id = $19, notified_at = $20, updated_at = now()
		WHERE institution_id = $1 AND id = $2`,
		m.InstitutionID, m.ID, m.Title, m.Kind, m.ScheduledAt, m.Modality, m.Place, m.MeetingURL, m.Agenda, m.Audience, m.Status, m.PreviousAt,
		m.RescheduleReason, m.CancelReason, m.Minutes, m.Agreements, m.Attendees, m.MinutesAt, optionalText(m.AnnouncementID), m.NotifiedAt)
	if err != nil {
		return domain.ApafaMeeting{}, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ApafaMeeting{}, domain.ErrNotFound
	}
	return r.Meeting(ctx, m.InstitutionID, m.ID)
}

func (r *ApafaRepository) DeleteMeeting(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM apafa.meetings WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err != nil {
		return academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *ApafaRepository) Board(ctx context.Context, institutionID string) ([]domain.ApafaBoardMember, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, institution_id, full_name, position, phone, starts_on, ends_on FROM apafa.board_members
		WHERE institution_id = $1
		ORDER BY ends_on DESC, array_position(ARRAY['president', 'vice_president', 'secretary', 'treasurer', 'spokesperson']::varchar[], position), full_name`, institutionID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ApafaBoardMember, error) {
		var b domain.ApafaBoardMember
		err := row.Scan(&b.ID, &b.InstitutionID, &b.FullName, &b.Position, &b.Phone, &b.StartsOn, &b.EndsOn)
		return b, err
	})
	if out == nil {
		out = []domain.ApafaBoardMember{}
	}
	return out, err
}

func (r *ApafaRepository) SaveBoardMember(ctx context.Context, b domain.ApafaBoardMember) (domain.ApafaBoardMember, error) {
	var err error
	if b.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO apafa.board_members (institution_id, full_name, position, phone, starts_on, ends_on)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, b.InstitutionID, b.FullName, b.Position, b.Phone, b.StartsOn, b.EndsOn).Scan(&b.ID)
	} else {
		var tag pgconn.CommandTag
		tag, err = r.c.pool.Exec(ctx, `UPDATE apafa.board_members SET full_name = $3, position = $4, phone = $5, starts_on = $6, ends_on = $7
			WHERE institution_id = $1 AND id = $2`, b.InstitutionID, b.ID, b.FullName, b.Position, b.Phone, b.StartsOn, b.EndsOn)
		if err == nil && tag.RowsAffected() == 0 {
			return b, domain.ErrNotFound
		}
	}
	return b, academicErr(err)
}

func (r *ApafaRepository) DeleteBoardMember(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM apafa.board_members WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err != nil {
		return academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *ApafaRepository) Fees(ctx context.Context, institutionID string) (domain.ApafaFees, error) {
	var f domain.ApafaFees
	err := r.c.pool.QueryRow(ctx, `WITH ch AS (
			SELECT ch.amount - ch.discount AS due, `+paidOf+` AS paid
			FROM finance.charges ch JOIN academic.periods pe ON pe.id = ch.period_id
			WHERE ch.institution_id = $1 AND ch.concept = 'apafa' AND ch.status = 'pending' AND pe.status <> 'closed'
		)
		SELECT round(coalesce(sum(due), 0) * 100)::bigint, round(coalesce(sum(least(paid, due)), 0) * 100)::bigint, count(*)::int,
			count(*) FILTER (WHERE paid >= due)::int FROM ch`, institutionID).Scan(&f.Expected, &f.Collected, &f.Charged, &f.Settled)
	f.Pending = f.Expected - f.Collected
	return f, err
}
