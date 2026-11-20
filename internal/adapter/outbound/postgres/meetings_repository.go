package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
)

// MeetingsRepository stores the live sessions' own data (live.joins, wellbeing.psych_appointments) and reads the
// live classes of the virtual classroom
type MeetingsRepository struct {
	c *Client
}

func NewMeetingsRepository(c *Client) *MeetingsRepository {
	return &MeetingsRepository{c: c}
}

// RecordJoin counts one more entry of the person into the call; the first one stays
func (r *MeetingsRepository) RecordJoin(ctx context.Context, institutionID, source, sourceID, userID, studentID, name string, at time.Time) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO live.joins (institution_id, source, source_id, user_id, student_id, display_name, first_at, last_at)
		VALUES ($1, $2, $3, $4, $5::uuid, $6, $7, $7)
		ON CONFLICT (source, source_id, user_id) DO UPDATE SET last_at = EXCLUDED.last_at, joins = live.joins.joins + 1,
			display_name = EXCLUDED.display_name, student_id = coalesce(EXCLUDED.student_id, live.joins.student_id)`,
		institutionID, source, sourceID, userID, nilIfEmpty(studentID), name, at)
	return academicErr(err)
}

func (r *MeetingsRepository) Joins(ctx context.Context, institutionID, source, sourceID string) ([]domain.LiveJoin, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT user_id, coalesce(student_id::text, ''), display_name, first_at, last_at, joins
		FROM live.joins WHERE institution_id = $1 AND source = $2 AND source_id = $3 ORDER BY first_at`, institutionID, source, sourceID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LiveJoin, error) {
		var j domain.LiveJoin
		var n int32
		err := row.Scan(&j.UserID, &j.StudentID, &j.DisplayName, &j.FirstAt, &j.LastAt, &n)
		j.Joins = int(n)
		return j, err
	})
	if out == nil {
		out = []domain.LiveJoin{}
	}
	return out, academicErr(err)
}

func (r *MeetingsRepository) LiveItems(ctx context.Context, institutionID string, roomIDs []string, from, to time.Time, publishedOnly bool, day time.Time) ([]domain.LiveTarget, error) {
	if len(roomIDs) == 0 {
		return []domain.LiveTarget{}, nil
	}
	rows, err := r.c.pool.Query(ctx, `SELECT i.id, i.room_id, i.title, i.starts_at, i.duration_minutes, i.provider, i.url
		FROM classroom.items i JOIN classroom.rooms r ON r.id = i.room_id JOIN classroom.units u ON u.id = i.unit_id
		WHERE r.institution_id = $1 AND i.room_id = ANY($2::uuid[]) AND i.kind = 'live'
		  AND i.starts_at >= $3 AND i.starts_at < $4
		  AND (NOT $5 OR (i.published AND u.published AND (u.opens_on IS NULL OR u.opens_on <= $6::date)))
		ORDER BY i.starts_at`, institutionID, roomIDs, from, to, publishedOnly, day)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LiveTarget, error) {
		t := domain.LiveTarget{InstitutionID: institutionID, Source: domain.SourceClass}
		var d int16
		err := row.Scan(&t.ID, &t.RoomID, &t.Title, &t.StartsAt, &d, &t.Provider, &t.Link)
		t.DurationMin = int(d)
		return t, err
	})
	if out == nil {
		out = []domain.LiveTarget{}
	}
	return out, academicErr(err)
}

const appointmentSelect = `SELECT a.id, a.case_id, a.student_id, s.given_names || ' ' || s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), ''), a.scheduled_at, a.duration_min, a.mode,
	a.provider, a.link, a.status, coalesce(a.host_user_id::text, ''), a.created_by_name
	FROM wellbeing.psych_appointments a JOIN people.students s ON s.id = a.student_id`

func scanAppointment(row pgx.Row) (domain.PsychAppointment, error) {
	var a domain.PsychAppointment
	var d int16
	err := row.Scan(&a.ID, &a.CaseID, &a.StudentID, &a.StudentName, &a.ScheduledAt, &d, &a.Mode, &a.Provider, &a.Link, &a.Status, &a.HostUserID, &a.CreatedName)
	a.DurationMin = int(d)
	return a, err
}

// SaveAppointment creates (the student comes from the case) or reschedules an appointment, which goes back to scheduled
func (r *MeetingsRepository) SaveAppointment(ctx context.Context, institutionID string, a domain.PsychAppointment) (domain.PsychAppointment, error) {
	id := a.ID
	var err error
	if id == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.psych_appointments (institution_id, case_id, student_id, scheduled_at, duration_min, mode, provider,
				link, host_user_id, created_by_name)
			SELECT $1, c.id, c.student_id, $3, $4, $5, $6, $7, $8::uuid, $9 FROM wellbeing.psych_cases c WHERE c.institution_id = $1 AND c.id = $2
			RETURNING id`, institutionID, a.CaseID, a.ScheduledAt, a.DurationMin, a.Mode, a.Provider, a.Link, nilIfEmpty(a.HostUserID), a.CreatedName).Scan(&id)
	} else {
		err = r.c.pool.QueryRow(ctx, `UPDATE wellbeing.psych_appointments SET scheduled_at = $3, duration_min = $4, mode = $5, provider = $6, link = $7,
				status = 'scheduled', host_user_id = coalesce($8::uuid, host_user_id)
			WHERE institution_id = $1 AND id = $2 RETURNING id`,
			institutionID, id, a.ScheduledAt, a.DurationMin, a.Mode, a.Provider, a.Link, nilIfEmpty(a.HostUserID)).Scan(&id)
	}
	if err != nil {
		return domain.PsychAppointment{}, academicErr(err)
	}
	return r.Appointment(ctx, institutionID, id)
}

func (r *MeetingsRepository) Appointment(ctx context.Context, institutionID, id string) (domain.PsychAppointment, error) {
	a, err := scanAppointment(r.c.pool.QueryRow(ctx, appointmentSelect+` WHERE a.institution_id = $1 AND a.id = $2`, institutionID, id))
	return a, academicErr(err)
}

func (r *MeetingsRepository) SetAppointmentStatus(ctx context.Context, institutionID, id, status string) (domain.PsychAppointment, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE wellbeing.psych_appointments SET status = $3 WHERE institution_id = $1 AND id = $2`, institutionID, id, status)
	if err != nil {
		return domain.PsychAppointment{}, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.PsychAppointment{}, domain.ErrNotFound
	}
	return r.Appointment(ctx, institutionID, id)
}

func (r *MeetingsRepository) Appointments(ctx context.Context, institutionID, caseID, studentID string, from, to *time.Time) ([]domain.PsychAppointment, error) {
	rows, err := r.c.pool.Query(ctx, appointmentSelect+` WHERE a.institution_id = $1
		  AND ($2 = '' OR a.case_id::text = $2) AND ($3 = '' OR a.student_id::text = $3)
		  AND ($4::timestamptz IS NULL OR a.scheduled_at >= $4) AND ($5::timestamptz IS NULL OR a.scheduled_at < $5)
		ORDER BY a.scheduled_at DESC`, institutionID, caseID, studentID, from, to)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PsychAppointment, error) { return scanAppointment(row) })
	if out == nil {
		out = []domain.PsychAppointment{}
	}
	return out, academicErr(err)
}
