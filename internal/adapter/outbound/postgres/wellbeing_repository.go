package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// WellbeingRepository implements port.WellbeingRepository
type WellbeingRepository struct{ c *Client }

func NewWellbeingRepository(c *Client) *WellbeingRepository { return &WellbeingRepository{c: c} }

var _ port.WellbeingRepository = (*WellbeingRepository)(nil)

// wellPlacement joins the current enrollment of a student s (active period, the latest) as x: section, level and
// the "3.° B · Secundaria" label
const wellPlacement = `JOIN LATERAL (SELECT e.section_id, e.level_id, e.period_id, ` + sectionLabel + ` AS label
		FROM enrollment.enrollments e JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active'
		JOIN academic.sections sec ON sec.id = e.section_id JOIN academic.levels l ON l.id = sec.level_id LEFT JOIN academic.grades g ON g.id = sec.grade_id
		WHERE e.student_id = s.id AND e.status = 'active' ORDER BY p.starts_on DESC LIMIT 1) x ON true`

// placementOf is the label of the current section of the student column given (empty when not enrolled)
func placementOf(studentCol string) string {
	return `coalesce((SELECT ` + sectionLabel + ` FROM enrollment.enrollments e JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active'
		JOIN academic.sections sec ON sec.id = e.section_id JOIN academic.levels l ON l.id = sec.level_id LEFT JOIN academic.grades g ON g.id = sec.grade_id
		WHERE e.student_id = ` + studentCol + ` AND e.status = 'active' ORDER BY p.starts_on DESC LIMIT 1), '')`
}

// nameOf is "Surnames, Given names" of the student column given
func nameOf(studentCol string) string {
	return `(SELECT s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names FROM people.students s WHERE s.id = ` + studentCol + `)`
}

func (r *WellbeingRepository) Students(ctx context.Context, f port.WellStudentFilter) ([]domain.WellStudent, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id, s.code, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names, x.label,
			x.section_id, x.level_id, s.special_needs,
			(SELECT count(*) FROM wellbeing.commitments c WHERE c.student_id = s.id AND c.status = 'pending'),
			(SELECT count(*) FROM wellbeing.incidents i WHERE i.student_id = s.id AND i.status = 'open'),
			EXISTS (SELECT 1 FROM wellbeing.psych_cases pc WHERE pc.student_id = s.id AND pc.status <> 'closed'),
			EXISTS (SELECT 1 FROM wellbeing.individual_plans ip WHERE ip.student_id = s.id AND ip.period_id = x.period_id),
			(SELECT max(iv.held_on) FROM wellbeing.interviews iv WHERE iv.student_id = s.id)
		FROM people.students s `+wellPlacement+`
		WHERE s.institution_id = $1 AND s.status = 'active'
			AND ($2::uuid[] IS NULL OR x.section_id = ANY($2)) AND ($3::uuid[] IS NULL OR s.id = ANY($3)) AND (NOT $5 OR s.special_needs)
			AND ($4 = '' OR `+fmt.Sprintf(fold, `s.given_names || ' ' || s.paternal_surname || ' ' || s.maternal_surname || ' ' || s.code || ' ' || s.document_number || ' ' || x.label`)+` LIKE '%' || `+fmt.Sprintf(fold, `$4`)+` || '%')
		ORDER BY x.label, s.paternal_surname, s.maternal_surname, s.given_names`, f.InstitutionID, uuids(f.SectionIDs), uuids(f.StudentIDs), f.Query, f.SpecialNeeds)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.WellStudent, error) {
		var w domain.WellStudent
		err := row.Scan(&w.ID, &w.Code, &w.Name, &w.Placement, &w.SectionID, &w.LevelID, &w.SpecialNeeds, &w.PendingCommitments, &w.OpenIncidents, &w.ActiveCase, &w.HasPlan, &w.LastInterview)
		return w, err
	})
}

// uuids turns a nil slice into SQL NULL ("any") and keeps an empty one empty ("none")
func uuids(ids []string) any {
	if ids == nil {
		return nil
	}
	return ids
}

func (r *WellbeingRepository) TeacherSections(ctx context.Context, institutionID, staffID string) ([]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT DISTINCT a.section_id FROM academic.assignments a JOIN academic.periods p ON p.id = a.period_id
		WHERE a.institution_id = $1 AND a.staff_id = $2 AND p.status = 'active'`, institutionID, staffID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (r *WellbeingRepository) TermOf(ctx context.Context, institutionID string, day time.Time) (string, time.Time, time.Time, error) {
	var id string
	var from, to time.Time
	err := r.c.pool.QueryRow(ctx, `SELECT t.id, t.starts_on, t.ends_on FROM academic.terms t JOIN academic.periods p ON p.id = t.period_id
		WHERE p.institution_id = $1 AND p.status = 'active' AND $2::date BETWEEN t.starts_on AND t.ends_on ORDER BY p.starts_on DESC LIMIT 1`, institutionID, date(day)).Scan(&id, &from, &to)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, time.Time{}, nil
	}
	return id, from, to, academicErr(err)
}

func (r *WellbeingRepository) CurrentPeriod(ctx context.Context, institutionID string, day time.Time) (string, string, error) {
	var id, name string
	err := r.c.pool.QueryRow(ctx, `SELECT id, name FROM academic.periods WHERE institution_id = $1 AND status = 'active'
		ORDER BY ($2::date BETWEEN starts_on AND ends_on) DESC, starts_on DESC LIMIT 1`, institutionID, date(day)).Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return id, name, academicErr(err)
}

// Specialists are the staff who may lead a POI: anyone with the specialist or psychologist role, or whose position
// names a therapy or special-education job
func (r *WellbeingRepository) Specialists(ctx context.Context, institutionID string) ([]domain.Staff, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT st.id, st.given_names, st.surnames, st.position, coalesce(st.email, '') FROM people.staff st
		WHERE st.institution_id = $1 AND st.status = 'active' AND (
			`+fmt.Sprintf(fold, "st.position")+` ~ '(terap|especialista|psicolog|saanee|prite|educacion especial)'
			OR EXISTS (SELECT 1 FROM iam.users u JOIN iam.user_roles ur ON ur.user_id = u.id JOIN iam.roles ro ON ro.id = ur.role_id
				WHERE lower(u.email) = lower(st.email) AND ur.institution_id = st.institution_id AND ro.code IN ('special_ed_specialist', 'psychologist')))
		ORDER BY st.surnames, st.given_names`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Staff, error) {
		var s domain.Staff
		err := row.Scan(&s.ID, &s.GivenNames, &s.Surnames, &s.Position, &s.Email)
		return s, err
	})
}

// ---------- tutoring ----------

const interviewColumns = `iv.id, iv.student_id, ` + `coalesce(` + `(SELECT s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names FROM people.students s WHERE s.id = iv.student_id)` + `, ''),
	iv.kind, iv.held_on, iv.topic, iv.notes, iv.agreements, coalesce(iv.author_id::text, ''), iv.author_name`

func scanInterview(row pgx.Row) (domain.Interview, error) {
	var x domain.Interview
	err := row.Scan(&x.ID, &x.StudentID, &x.StudentName, &x.Kind, &x.HeldOn, &x.Topic, &x.Notes, &x.Agreements, &x.AuthorID, &x.AuthorName)
	return x, err
}

func (r *WellbeingRepository) Interviews(ctx context.Context, institutionID string, studentIDs []string, from *time.Time) ([]domain.Interview, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+interviewColumns+` FROM wellbeing.interviews iv
		WHERE iv.institution_id = $1 AND ($2::uuid[] IS NULL OR iv.student_id = ANY($2)) AND ($3::date IS NULL OR iv.held_on >= $3)
		ORDER BY iv.held_on DESC, iv.created_at DESC LIMIT 500`, institutionID, uuids(studentIDs), from)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Interview, error) { return scanInterview(row) })
}

func (r *WellbeingRepository) Interview(ctx context.Context, institutionID, id string) (domain.Interview, error) {
	x, err := scanInterview(r.c.pool.QueryRow(ctx, `SELECT `+interviewColumns+` FROM wellbeing.interviews iv WHERE iv.institution_id = $1 AND iv.id = $2`, institutionID, id))
	return x, academicErr(err)
}

func (r *WellbeingRepository) SaveInterview(ctx context.Context, institutionID string, x domain.Interview) (domain.Interview, error) {
	var id string
	var err error
	if x.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.interviews (institution_id, student_id, kind, held_on, topic, notes, agreements, author_id, author_name)
			VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::uuid, $9) RETURNING id`,
			institutionID, x.StudentID, x.Kind, date(x.HeldOn), x.Topic, x.Notes, x.Agreements, x.AuthorID, x.AuthorName).Scan(&id)
	} else {
		id = x.ID
		err = r.exec(ctx, `UPDATE wellbeing.interviews SET kind = $3, held_on = $4, topic = $5, notes = $6, agreements = $7 WHERE institution_id = $1 AND id = $2`,
			institutionID, x.ID, x.Kind, date(x.HeldOn), x.Topic, x.Notes, x.Agreements)
	}
	if err != nil {
		return domain.Interview{}, academicErr(err)
	}
	return r.Interview(ctx, institutionID, id)
}

func (r *WellbeingRepository) DeleteInterview(ctx context.Context, institutionID, id string) error {
	return r.exec(ctx, `DELETE FROM wellbeing.interviews WHERE institution_id = $1 AND id = $2`, institutionID, id)
}

// exec runs a statement that must touch one row
func (r *WellbeingRepository) exec(ctx context.Context, sql string, args ...any) error {
	tag, err := r.c.pool.Exec(ctx, sql, args...)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

const commitmentColumns = `c.id, c.student_id, coalesce(` + `(SELECT s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names FROM people.students s WHERE s.id = c.student_id)` + `, ''),
	coalesce(c.interview_id::text, ''), c.party, c.description, c.due_on, c.status, c.checked_on, c.author_name`

func scanCommitment(row pgx.Row, extra ...any) (domain.Commitment, error) {
	var c domain.Commitment
	err := row.Scan(append([]any{&c.ID, &c.StudentID, &c.StudentName, &c.InterviewID, &c.Party, &c.Description, &c.DueOn, &c.Status, &c.CheckedOn, &c.AuthorName}, extra...)...)
	return c, err
}

func (r *WellbeingRepository) Commitments(ctx context.Context, institutionID string, studentIDs []string, pendingOnly bool) ([]domain.Commitment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+commitmentColumns+`, `+placementOf("c.student_id")+` FROM wellbeing.commitments c
		WHERE c.institution_id = $1 AND ($2::uuid[] IS NULL OR c.student_id = ANY($2)) AND (NOT $3 OR c.status = 'pending')
		ORDER BY (c.status = 'pending') DESC, c.due_on DESC LIMIT 1000`, institutionID, uuids(studentIDs), pendingOnly)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Commitment, error) {
		var placement string
		c, err := scanCommitment(row, &placement)
		c.Placement = placement
		return c, err
	})
}

func (r *WellbeingRepository) Commitment(ctx context.Context, institutionID, id string) (domain.Commitment, error) {
	c, err := scanCommitment(r.c.pool.QueryRow(ctx, `SELECT `+commitmentColumns+` FROM wellbeing.commitments c WHERE c.institution_id = $1 AND c.id = $2`, institutionID, id))
	return c, academicErr(err)
}

func (r *WellbeingRepository) SaveCommitment(ctx context.Context, institutionID string, c domain.Commitment) (domain.Commitment, error) {
	var id string
	if c.ID == "" {
		if err := r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.commitments (institution_id, student_id, interview_id, party, description, due_on, author_name)
			VALUES ($1, $2, nullif($3, '')::uuid, $4, $5, $6, $7) RETURNING id`, institutionID, c.StudentID, c.InterviewID, c.Party, c.Description, date(c.DueOn), c.AuthorName).Scan(&id); err != nil {
			return domain.Commitment{}, academicErr(err)
		}
	} else {
		id = c.ID
		if err := r.exec(ctx, `UPDATE wellbeing.commitments SET party = $3, description = $4, due_on = $5 WHERE institution_id = $1 AND id = $2`,
			institutionID, c.ID, c.Party, c.Description, date(c.DueOn)); err != nil {
			return domain.Commitment{}, err
		}
	}
	return r.Commitment(ctx, institutionID, id)
}

func (r *WellbeingRepository) CheckCommitment(ctx context.Context, institutionID, id, status string, on *time.Time) error {
	return r.exec(ctx, `UPDATE wellbeing.commitments SET status = $3, checked_on = $4 WHERE institution_id = $1 AND id = $2`, institutionID, id, status, on)
}

func (r *WellbeingRepository) DeleteCommitment(ctx context.Context, institutionID, id string) error {
	return r.exec(ctx, `DELETE FROM wellbeing.commitments WHERE institution_id = $1 AND id = $2`, institutionID, id)
}

// ---------- psychology ----------

const caseColumns = `pc.id, pc.student_id, coalesce(` + `(SELECT s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names FROM people.students s WHERE s.id = pc.student_id)` + `, ''),
	pc.origin, coalesce(pc.referred_by::text, ''), pc.referred_name, pc.reason, pc.priority, pc.status, pc.opened_on, pc.closed_on, pc.psychologist_name, pc.shared_note,
	(SELECT count(*) FROM wellbeing.psych_sessions ps WHERE ps.case_id = pc.id), (SELECT max(ps.held_on) FROM wellbeing.psych_sessions ps WHERE ps.case_id = pc.id)`

func scanCase(row pgx.Row, extra ...any) (domain.PsychCase, error) {
	var c domain.PsychCase
	err := row.Scan(append([]any{&c.ID, &c.StudentID, &c.StudentName, &c.Origin, &c.ReferredBy, &c.ReferredName, &c.Reason, &c.Priority, &c.Status, &c.OpenedOn, &c.ClosedOn,
		&c.PsychologistName, &c.SharedNote, &c.Sessions, &c.LastSession}, extra...)...)
	return c, err
}

func (r *WellbeingRepository) Cases(ctx context.Context, institutionID string, studentIDs []string, status, referredBy string) ([]domain.PsychCase, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+caseColumns+`, `+placementOf("pc.student_id")+` FROM wellbeing.psych_cases pc
		WHERE pc.institution_id = $1 AND ($2::uuid[] IS NULL OR pc.student_id = ANY($2))
			AND ($3 = '' OR pc.status = $3 OR ($3 = 'active' AND pc.status <> 'closed')) AND ($4 = '' OR pc.referred_by = nullif($4, '')::uuid)
		ORDER BY (pc.status = 'closed'), (pc.priority = 'urgent') DESC, pc.opened_on DESC LIMIT 1000`, institutionID, uuids(studentIDs), status, referredBy)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PsychCase, error) {
		var placement string
		c, err := scanCase(row, &placement)
		c.Placement = placement
		return c, err
	})
}

func (r *WellbeingRepository) Case(ctx context.Context, institutionID, id string) (domain.PsychCase, error) {
	var placement string
	c, err := scanCase(r.c.pool.QueryRow(ctx, `SELECT `+caseColumns+`, `+placementOf("pc.student_id")+` FROM wellbeing.psych_cases pc WHERE pc.institution_id = $1 AND pc.id = $2`, institutionID, id), &placement)
	c.Placement = placement
	return c, academicErr(err)
}

func (r *WellbeingRepository) OpenCase(ctx context.Context, institutionID string, c domain.PsychCase) (domain.PsychCase, error) {
	var id string
	if err := r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.psych_cases (institution_id, student_id, origin, referred_by, referred_name, reason, priority, status, opened_on, psychologist_name)
		VALUES ($1, $2, $3, nullif($4, '')::uuid, $5, $6, $7, $8, $9, $10) RETURNING id`,
		institutionID, c.StudentID, c.Origin, c.ReferredBy, c.ReferredName, c.Reason, c.Priority, c.Status, date(c.OpenedOn), c.PsychologistName).Scan(&id); err != nil {
		return domain.PsychCase{}, academicErr(err)
	}
	return r.Case(ctx, institutionID, id)
}

func (r *WellbeingRepository) UpdateCase(ctx context.Context, institutionID string, c domain.PsychCase) error {
	return r.exec(ctx, `UPDATE wellbeing.psych_cases SET status = $3, priority = $4, shared_note = $5, psychologist_name = $6, closed_on = $7, reason = $8, updated_at = now()
		WHERE institution_id = $1 AND id = $2`, institutionID, c.ID, c.Status, c.Priority, c.SharedNote, c.PsychologistName, c.ClosedOn, c.Reason)
}

func (r *WellbeingRepository) Sessions(ctx context.Context, caseID string) ([]domain.PsychSession, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, case_id, held_on, kind, notes, next_steps, author_name FROM wellbeing.psych_sessions WHERE case_id = $1 ORDER BY held_on DESC, created_at DESC`, caseID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PsychSession, error) {
		var s domain.PsychSession
		err := row.Scan(&s.ID, &s.CaseID, &s.HeldOn, &s.Kind, &s.Notes, &s.NextSteps, &s.AuthorName)
		return s, err
	})
}

func (r *WellbeingRepository) AddSession(ctx context.Context, s domain.PsychSession) (domain.PsychSession, error) {
	err := r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.psych_sessions (case_id, held_on, kind, notes, next_steps, author_name) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		s.CaseID, date(s.HeldOn), s.Kind, s.Notes, s.NextSteps, s.AuthorName).Scan(&s.ID)
	return s, academicErr(err)
}

func (r *WellbeingRepository) DeleteSession(ctx context.Context, caseID, id string) error {
	return r.exec(ctx, `DELETE FROM wellbeing.psych_sessions WHERE case_id = $1 AND id = $2`, caseID, id)
}

// ---------- discipline ----------

const incidentColumns = `i.id, i.student_id, coalesce(` + `(SELECT s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names FROM people.students s WHERE s.id = i.student_id)` + `, ''),
	i.occurred_on, i.severity, i.category, i.description, coalesce(i.reported_by::text, ''), i.reported_name, i.family_notified_on, i.status`

func scanIncident(row pgx.Row, extra ...any) (domain.Incident, error) {
	var x domain.Incident
	err := row.Scan(append([]any{&x.ID, &x.StudentID, &x.StudentName, &x.OccurredOn, &x.Severity, &x.Category, &x.Description, &x.ReportedBy, &x.ReportedName, &x.FamilyNotifiedOn, &x.Status}, extra...)...)
	return x, err
}

const measureColumns = `m.id, m.student_id, coalesce(m.incident_id::text, ''), m.kind, m.ground, m.detail, m.starts_on, m.days, m.decided_name, m.notified_on,
	m.reconsideration_requested_on, m.reconsideration_reason, m.reconsideration_by, m.resolved_on, m.outcome, m.resolution`

func scanMeasure(row pgx.Row) (domain.Measure, error) {
	var m domain.Measure
	var days int16
	err := row.Scan(&m.ID, &m.StudentID, &m.IncidentID, &m.Kind, &m.Ground, &m.Detail, &m.StartsOn, &days, &m.DecidedName, &m.NotifiedOn,
		&m.ReconsiderationOn, &m.ReconsiderationReason, &m.ReconsiderationBy, &m.ResolvedOn, &m.Outcome, &m.Resolution)
	m.Days = int(days)
	return m, err
}

func (r *WellbeingRepository) Incidents(ctx context.Context, f domain.IncidentFilter) ([]domain.Incident, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+incidentColumns+`, `+placementOf("i.student_id")+` FROM wellbeing.incidents i
		WHERE i.institution_id = $1 AND ($2::uuid[] IS NULL OR i.student_id = ANY($2)) AND ($3::date IS NULL OR i.occurred_on >= $3) AND ($4::date IS NULL OR i.occurred_on <= $4)
			AND ($5 = '' OR i.severity = $5) AND ($6 = '' OR i.status = $6)
		ORDER BY i.occurred_on DESC, i.created_at DESC LIMIT 1000`, f.InstitutionID, uuids(f.StudentIDs), f.From, f.To, f.Severity, f.Status)
	if err != nil {
		return nil, academicErr(err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Incident, error) {
		var placement string
		x, err := scanIncident(row, &placement)
		x.Placement = placement
		return x, err
	})
	if err != nil || len(list) == 0 {
		return list, err
	}
	ids := make([]string, len(list))
	at := make(map[string]int, len(list))
	for i, x := range list {
		ids[i], at[x.ID] = x.ID, i
	}
	rows, err = r.c.pool.Query(ctx, `SELECT `+measureColumns+` FROM wellbeing.measures m WHERE m.incident_id = ANY($1) ORDER BY m.starts_on, m.created_at`, ids)
	if err != nil {
		return nil, academicErr(err)
	}
	measures, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Measure, error) { return scanMeasure(row) })
	for _, m := range measures {
		list[at[m.IncidentID]].Measures = append(list[at[m.IncidentID]].Measures, m)
	}
	return list, err
}

func (r *WellbeingRepository) Incident(ctx context.Context, institutionID, id string) (domain.Incident, error) {
	var placement string
	x, err := scanIncident(r.c.pool.QueryRow(ctx, `SELECT `+incidentColumns+`, `+placementOf("i.student_id")+` FROM wellbeing.incidents i WHERE i.institution_id = $1 AND i.id = $2`, institutionID, id), &placement)
	if err != nil {
		return domain.Incident{}, academicErr(err)
	}
	x.Placement = placement
	rows, err := r.c.pool.Query(ctx, `SELECT `+measureColumns+` FROM wellbeing.measures m WHERE m.incident_id = $1 ORDER BY m.starts_on, m.created_at`, id)
	if err != nil {
		return domain.Incident{}, academicErr(err)
	}
	x.Measures, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Measure, error) { return scanMeasure(row) })
	return x, err
}

func (r *WellbeingRepository) SaveIncident(ctx context.Context, institutionID string, x domain.Incident) (domain.Incident, error) {
	id := x.ID
	if id == "" {
		if err := r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.incidents (institution_id, student_id, occurred_on, severity, category, description, reported_by, reported_name, family_notified_on, status)
			VALUES ($1, $2, $3, $4, $5, $6, nullif($7, '')::uuid, $8, $9, $10) RETURNING id`,
			institutionID, x.StudentID, date(x.OccurredOn), x.Severity, x.Category, x.Description, x.ReportedBy, x.ReportedName, x.FamilyNotifiedOn, x.Status).Scan(&id); err != nil {
			return domain.Incident{}, academicErr(err)
		}
	} else if err := r.exec(ctx, `UPDATE wellbeing.incidents SET occurred_on = $3, severity = $4, category = $5, description = $6, family_notified_on = $7, status = $8
		WHERE institution_id = $1 AND id = $2`, institutionID, id, date(x.OccurredOn), x.Severity, x.Category, x.Description, x.FamilyNotifiedOn, x.Status); err != nil {
		return domain.Incident{}, err
	}
	return r.Incident(ctx, institutionID, id)
}

func (r *WellbeingRepository) DeleteIncident(ctx context.Context, institutionID, id string) error {
	return r.exec(ctx, `DELETE FROM wellbeing.incidents WHERE institution_id = $1 AND id = $2`, institutionID, id)
}

func (r *WellbeingRepository) Measures(ctx context.Context, institutionID, studentID string, from time.Time) ([]domain.Measure, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+measureColumns+` FROM wellbeing.measures m WHERE m.institution_id = $1 AND m.student_id = $2 AND m.starts_on >= $3
		ORDER BY m.starts_on, m.created_at`, institutionID, studentID, date(from))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Measure, error) { return scanMeasure(row) })
}

func (r *WellbeingRepository) Measure(ctx context.Context, institutionID, id string) (domain.Measure, error) {
	m, err := scanMeasure(r.c.pool.QueryRow(ctx, `SELECT `+measureColumns+` FROM wellbeing.measures m WHERE m.institution_id = $1 AND m.id = $2`, institutionID, id))
	return m, academicErr(err)
}

func (r *WellbeingRepository) MeasureRows(ctx context.Context, institutionID string, studentIDs []string, from time.Time) ([]port.MeasureRow, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+measureColumns+`, `+nameOf("m.student_id")+`, `+placementOf("m.student_id")+` FROM wellbeing.measures m
		WHERE m.institution_id = $1 AND m.kind <> 'corrective' AND ($2::uuid[] IS NULL OR m.student_id = ANY($2)) AND m.starts_on >= $3
		ORDER BY (m.reconsideration_requested_on IS NOT NULL AND m.resolved_on IS NULL) DESC, m.starts_on DESC LIMIT 500`, institutionID, uuids(studentIDs), date(from))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (port.MeasureRow, error) {
		var m port.MeasureRow
		var days int16
		err := row.Scan(&m.ID, &m.StudentID, &m.IncidentID, &m.Kind, &m.Ground, &m.Detail, &m.StartsOn, &days, &m.DecidedName, &m.NotifiedOn,
			&m.ReconsiderationOn, &m.ReconsiderationReason, &m.ReconsiderationBy, &m.ResolvedOn, &m.Outcome, &m.Resolution, &m.StudentName, &m.Placement)
		m.Days = int(days)
		return m, err
	})
}

func (r *WellbeingRepository) AddMeasure(ctx context.Context, institutionID string, m domain.Measure) (domain.Measure, error) {
	var id string
	if err := r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.measures (institution_id, student_id, incident_id, kind, ground, detail, starts_on, days, decided_name, notified_on)
		VALUES ($1, $2, nullif($3, '')::uuid, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		institutionID, m.StudentID, m.IncidentID, m.Kind, m.Ground, m.Detail, date(m.StartsOn), m.Days, m.DecidedName, m.NotifiedOn).Scan(&id); err != nil {
		return domain.Measure{}, academicErr(err)
	}
	return r.Measure(ctx, institutionID, id)
}

func (r *WellbeingRepository) NotifyMeasure(ctx context.Context, institutionID, id string, on time.Time) error {
	return r.exec(ctx, `UPDATE wellbeing.measures SET notified_on = $3 WHERE institution_id = $1 AND id = $2`, institutionID, id, date(on))
}

func (r *WellbeingRepository) RequestReconsideration(ctx context.Context, institutionID, id string, on time.Time, reason, by string) error {
	return r.exec(ctx, `UPDATE wellbeing.measures SET reconsideration_requested_on = $3, reconsideration_reason = $4, reconsideration_by = $5
		WHERE institution_id = $1 AND id = $2 AND reconsideration_requested_on IS NULL`, institutionID, id, date(on), reason, by)
}

func (r *WellbeingRepository) ResolveReconsideration(ctx context.Context, institutionID, id string, on time.Time, outcome, resolution string) error {
	return r.exec(ctx, `UPDATE wellbeing.measures SET resolved_on = $3, outcome = $4, resolution = $5
		WHERE institution_id = $1 AND id = $2 AND reconsideration_requested_on IS NOT NULL`, institutionID, id, date(on), outcome, resolution)
}

func (r *WellbeingRepository) DeleteMeasure(ctx context.Context, institutionID, id string) error {
	return r.exec(ctx, `DELETE FROM wellbeing.measures WHERE institution_id = $1 AND id = $2`, institutionID, id)
}

func (r *WellbeingRepository) CountIncidents(ctx context.Context, institutionID, studentID, severity string, from, to time.Time) (int, error) {
	var n int
	err := r.c.pool.QueryRow(ctx, `SELECT count(*) FROM wellbeing.incidents WHERE institution_id = $1 AND student_id = $2 AND severity = $3 AND occurred_on BETWEEN $4 AND $5`,
		institutionID, studentID, severity, date(from), date(to)).Scan(&n)
	return n, academicErr(err)
}

func (r *WellbeingRepository) Conduct(ctx context.Context, institutionID, studentID, periodID string) ([]domain.TermConduct, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT coalesce(t.id::text, ''), coalesce(t.name, p.name), cm.score, cm.note FROM assessment.conduct_marks cm
		JOIN academic.sections sec ON sec.id = cm.section_id JOIN academic.periods p ON p.id = sec.period_id LEFT JOIN academic.terms t ON t.id = cm.term_id
		WHERE cm.institution_id = $1 AND cm.student_id = $2 AND ($3 = '' OR p.id = nullif($3, '')::uuid) ORDER BY p.starts_on, t.number`, institutionID, studentID, periodID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TermConduct, error) {
		var m domain.TermConduct
		var score int16
		err := row.Scan(&m.TermID, &m.TermName, &score, &m.Note)
		m.Score = int(score)
		return m, err
	})
}

func (r *WellbeingRepository) LowConduct(ctx context.Context, institutionID, periodID string, sectionIDs []string) ([]port.LowConduct, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT DISTINCT ON (cm.student_id) cm.student_id, `+nameOf("cm.student_id")+`, `+placementOf("cm.student_id")+`, cm.section_id, cm.score, coalesce(t.name, '')
		FROM assessment.conduct_marks cm JOIN academic.sections sec ON sec.id = cm.section_id LEFT JOIN academic.terms t ON t.id = cm.term_id
		WHERE cm.institution_id = $1 AND sec.period_id = $2 AND ($3::uuid[] IS NULL OR cm.section_id = ANY($3))
		ORDER BY cm.student_id, t.number DESC NULLS LAST`, institutionID, periodID, uuids(sectionIDs))
	if err != nil {
		return nil, academicErr(err)
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (port.LowConduct, error) {
		var l port.LowConduct
		var score int16
		err := row.Scan(&l.StudentID, &l.StudentName, &l.Placement, &l.SectionID, &score, &l.TermName)
		l.Score = int(score)
		return l, err
	})
	low := all[:0]
	for _, l := range all {
		if l.Score < 11 {
			low = append(low, l)
		}
	}
	return low, err
}

// ---------- special education ----------

const planColumns = `ip.id, ip.student_id, coalesce(` + `(SELECT s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names FROM people.students s WHERE s.id = ip.student_id)` + `, ''),
	ip.period_id, p.name, coalesce(ip.specialist_id::text, ''), coalesce(st.given_names || ' ' || st.surnames, ''),
	ip.needs, ip.strengths, ip.educational_response, ip.curricular_adaptations, ip.access_adaptations, ip.evaluation_criteria, ip.actors, ip.family_commitments,
	ip.status, ip.approved_name, ip.approved_on, ip.updated_at,
	(SELECT count(*) FROM wellbeing.therapy_sessions ts WHERE ts.student_id = ip.student_id AND ts.held_on BETWEEN p.starts_on AND p.ends_on), ` + `coalesce((SELECT ` + sectionLabel + ` FROM enrollment.enrollments e
		JOIN academic.sections sec ON sec.id = e.section_id JOIN academic.levels l ON l.id = sec.level_id LEFT JOIN academic.grades g ON g.id = sec.grade_id
		WHERE e.student_id = ip.student_id AND e.period_id = ip.period_id ORDER BY e.enrolled_on DESC LIMIT 1), '')`

const planFrom = ` FROM wellbeing.individual_plans ip JOIN academic.periods p ON p.id = ip.period_id LEFT JOIN people.staff st ON st.id = ip.specialist_id`

func scanPlan(row pgx.Row) (domain.IndividualPlan, error) {
	var x domain.IndividualPlan
	err := row.Scan(&x.ID, &x.StudentID, &x.StudentName, &x.PeriodID, &x.PeriodName, &x.SpecialistID, &x.SpecialistName,
		&x.Needs, &x.Strengths, &x.EducationalResponse, &x.CurricularAdaptations, &x.AccessAdaptations, &x.EvaluationCriteria, &x.Actors, &x.FamilyCommitments,
		&x.Status, &x.ApprovedName, &x.ApprovedOn, &x.UpdatedAt, &x.Therapies, &x.Placement)
	return x, err
}

func (r *WellbeingRepository) Plans(ctx context.Context, institutionID, periodID, specialistID string, studentIDs []string) ([]domain.IndividualPlan, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+planColumns+planFrom+`
		WHERE ip.institution_id = $1 AND ($2 = '' OR ip.period_id = nullif($2, '')::uuid) AND ($3 = '' OR ip.specialist_id = nullif($3, '')::uuid)
			AND ($4::uuid[] IS NULL OR ip.student_id = ANY($4))
		ORDER BY p.starts_on DESC, 21, 3`, institutionID, periodID, specialistID, uuids(studentIDs))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.IndividualPlan, error) { return scanPlan(row) })
}

func (r *WellbeingRepository) Plan(ctx context.Context, institutionID, id string) (domain.IndividualPlan, error) {
	x, err := scanPlan(r.c.pool.QueryRow(ctx, `SELECT `+planColumns+planFrom+` WHERE ip.institution_id = $1 AND ip.id = $2`, institutionID, id))
	return x, academicErr(err)
}

func (r *WellbeingRepository) SavePlan(ctx context.Context, institutionID string, x domain.IndividualPlan) (domain.IndividualPlan, error) {
	id := x.ID
	if id == "" {
		if err := r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.individual_plans (institution_id, student_id, period_id, specialist_id, needs, strengths, educational_response,
				curricular_adaptations, access_adaptations, evaluation_criteria, actors, family_commitments)
			VALUES ($1, $2, $3, nullif($4, '')::uuid, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
			institutionID, x.StudentID, x.PeriodID, x.SpecialistID, x.Needs, x.Strengths, x.EducationalResponse, x.CurricularAdaptations, x.AccessAdaptations,
			x.EvaluationCriteria, x.Actors, x.FamilyCommitments).Scan(&id); err != nil {
			return domain.IndividualPlan{}, wellbeingErr(err)
		}
	} else if err := r.exec(ctx, `UPDATE wellbeing.individual_plans SET specialist_id = nullif($3, '')::uuid, needs = $4, strengths = $5, educational_response = $6,
			curricular_adaptations = $7, access_adaptations = $8, evaluation_criteria = $9, actors = $10, family_commitments = $11, updated_at = now()
		WHERE institution_id = $1 AND id = $2`, institutionID, id, x.SpecialistID, x.Needs, x.Strengths, x.EducationalResponse, x.CurricularAdaptations,
		x.AccessAdaptations, x.EvaluationCriteria, x.Actors, x.FamilyCommitments); err != nil {
		return domain.IndividualPlan{}, err
	}
	return r.Plan(ctx, institutionID, id)
}

func wellbeingErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.ConstraintName == "individual_plans_key" {
		return domain.Validation(map[string]string{"studentId": "El estudiante ya tiene un plan en ese periodo."})
	}
	return academicErr(err)
}

func (r *WellbeingRepository) SetPlanStatus(ctx context.Context, institutionID, id, status, name string, on *time.Time) error {
	return r.exec(ctx, `UPDATE wellbeing.individual_plans SET status = $3, approved_name = $4, approved_on = $5, updated_at = now() WHERE institution_id = $1 AND id = $2`,
		institutionID, id, status, name, on)
}

func (r *WellbeingRepository) DeletePlan(ctx context.Context, institutionID, id string) error {
	return r.exec(ctx, `DELETE FROM wellbeing.individual_plans WHERE institution_id = $1 AND id = $2`, institutionID, id)
}

const therapyColumns = `ts.id, ts.student_id, coalesce(` + `(SELECT s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names FROM people.students s WHERE s.id = ts.student_id)` + `, ''),
	coalesce(ts.therapist_id::text, ''), ts.therapist_name, ts.kind, ts.held_on, ts.minutes, ts.attended, ts.goal, ts.notes, ts.home_tasks`

func scanTherapy(row pgx.Row) (domain.TherapySession, error) {
	var t domain.TherapySession
	var minutes int16
	err := row.Scan(&t.ID, &t.StudentID, &t.StudentName, &t.TherapistID, &t.TherapistName, &t.Kind, &t.HeldOn, &minutes, &t.Attended, &t.Goal, &t.Notes, &t.HomeTasks)
	t.Minutes = int(minutes)
	return t, err
}

func (r *WellbeingRepository) Therapies(ctx context.Context, f port.TherapyFilter) ([]domain.TherapySession, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+therapyColumns+` FROM wellbeing.therapy_sessions ts
		WHERE ts.institution_id = $1 AND ($2::uuid[] IS NULL OR ts.student_id = ANY($2)) AND ($3 = '' OR ts.therapist_id = nullif($3, '')::uuid)
			AND ($4::date IS NULL OR ts.held_on >= $4) AND ($5::date IS NULL OR ts.held_on <= $5)
		ORDER BY ts.held_on DESC, ts.created_at DESC LIMIT 1000`, f.InstitutionID, uuids(f.StudentIDs), f.TherapistID, f.From, f.To)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TherapySession, error) { return scanTherapy(row) })
}

func (r *WellbeingRepository) Therapy(ctx context.Context, institutionID, id string) (domain.TherapySession, error) {
	t, err := scanTherapy(r.c.pool.QueryRow(ctx, `SELECT `+therapyColumns+` FROM wellbeing.therapy_sessions ts WHERE ts.institution_id = $1 AND ts.id = $2`, institutionID, id))
	return t, academicErr(err)
}

func (r *WellbeingRepository) SaveTherapy(ctx context.Context, institutionID string, t domain.TherapySession) (domain.TherapySession, error) {
	id := t.ID
	if id == "" {
		if err := r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.therapy_sessions (institution_id, student_id, therapist_id, therapist_name, kind, held_on, minutes, attended, goal, notes, home_tasks)
			VALUES ($1, $2, nullif($3, '')::uuid, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
			institutionID, t.StudentID, t.TherapistID, t.TherapistName, t.Kind, date(t.HeldOn), t.Minutes, t.Attended, t.Goal, t.Notes, t.HomeTasks).Scan(&id); err != nil {
			return domain.TherapySession{}, academicErr(err)
		}
	} else if err := r.exec(ctx, `UPDATE wellbeing.therapy_sessions SET kind = $3, held_on = $4, minutes = $5, attended = $6, goal = $7, notes = $8, home_tasks = $9
		WHERE institution_id = $1 AND id = $2`, institutionID, id, t.Kind, date(t.HeldOn), t.Minutes, t.Attended, t.Goal, t.Notes, t.HomeTasks); err != nil {
		return domain.TherapySession{}, err
	}
	return r.Therapy(ctx, institutionID, id)
}

func (r *WellbeingRepository) DeleteTherapy(ctx context.Context, institutionID, id string) error {
	return r.exec(ctx, `DELETE FROM wellbeing.therapy_sessions WHERE institution_id = $1 AND id = $2`, institutionID, id)
}

// ---------- demo seed ----------

func (r *WellbeingRepository) HasWellbeing(ctx context.Context, institutionID string) (bool, error) {
	var any bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wellbeing.interviews WHERE institution_id = $1)
		OR EXISTS (SELECT 1 FROM wellbeing.individual_plans WHERE institution_id = $1)`, institutionID).Scan(&any)
	return any, academicErr(err)
}

// SeedTutors maps each section of the active periods to the name of its tutor
func (r *WellbeingRepository) SeedTutors(ctx context.Context, institutionID string) (map[string]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT sec.id, st.given_names || ' ' || st.surnames FROM academic.sections sec JOIN academic.periods p ON p.id = sec.period_id
		JOIN people.staff st ON st.id = sec.tutor_id WHERE p.institution_id = $1 AND p.status = 'active'`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// SeedStaffNamed is the name of the first active staff member whose position starts with prefix
func (r *WellbeingRepository) SeedStaffNamed(ctx context.Context, institutionID, prefix string) (string, error) {
	var name string
	err := r.c.pool.QueryRow(ctx, `SELECT given_names || ' ' || surnames FROM people.staff WHERE institution_id = $1 AND status = 'active' AND position ILIKE $2 || '%'
		ORDER BY code LIMIT 1`, institutionID, prefix).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, academicErr(err)
}

// SeedStaff adds a staff member (demo therapists) and returns its id; an existing code is reused
func (r *WellbeingRepository) SeedStaff(ctx context.Context, institutionID, code, dni, given, surnames, email, position string) (string, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO people.staff (institution_id, code, document_type, document_number, given_names, surnames, email, position, hired_on)
		VALUES ($1, $2, 'dni', $3, $4, $5, $6, $7, '2024-03-01') ON CONFLICT (institution_id, code) DO UPDATE SET position = EXCLUDED.position RETURNING id`,
		institutionID, code, dni, given, surnames, email, position).Scan(&id)
	return id, academicErr(err)
}

// SeedConduct sets the latest conduct grade of a student (the demo's failing cases)
func (r *WellbeingRepository) SeedConduct(ctx context.Context, institutionID, studentID string, score int, note string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE assessment.conduct_marks SET score = $3, note = $4 WHERE id = (
		SELECT cm.id FROM assessment.conduct_marks cm LEFT JOIN academic.terms t ON t.id = cm.term_id
		WHERE cm.institution_id = $1 AND cm.student_id = $2 ORDER BY t.number DESC NULLS LAST LIMIT 1)`, institutionID, studentID, score, note)
	return academicErr(err)
}
