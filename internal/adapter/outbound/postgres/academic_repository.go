package postgres

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// AcademicRepository implements port.AcademicRepository over the academic schema
type AcademicRepository struct {
	c *Client
}

func NewAcademicRepository(c *Client) *AcademicRepository {
	return &AcademicRepository{c: c}
}

var _ port.AcademicRepository = (*AcademicRepository)(nil)

// academicErr maps constraint failures to the errors the panel shows
func academicErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return domain.ErrDuplicateName
		case "22P02": // malformed uuid: an id that cannot exist
			return domain.ErrNotFound
		case "23514":
			// Tuition in a public institution (triggers of migration 0022)
			if pg.ConstraintName == "public_no_tuition" {
				return domain.ErrPublicNoTuition
			}
		}
	}
	return err
}

// date keeps only the calendar day (columns are date, values travel as midnight UTC)
func date(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (r *AcademicRepository) InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error) {
	var i domain.Institution
	err := r.c.pool.QueryRow(ctx, `SELECT id, slug, name, kind FROM iam.institutions WHERE slug = $1 AND is_active`, slug).
		Scan(&i.ID, &i.Slug, &i.Name, &i.Kind)
	return i, notFound(err)
}

// ---------- settings ----------

func (r *AcademicRepository) Settings(ctx context.Context, institutionID string) (domain.AcademicSettings, error) {
	var s domain.AcademicSettings
	var passing *int16
	var days []int16
	err := r.c.pool.QueryRow(ctx, `SELECT term_scheme, grading_scale, passing_grade, conduct_grade, attention_modes, shifts, week_days, late_after_minutes, absence_limit_percent,
			dre, ugel, modular_code, logo, management, updated_at
		FROM academic.settings WHERE institution_id = $1`, institutionID).
		Scan(&s.TermScheme, &s.GradingScale, &passing, &s.ConductGrade, &s.AttentionModes, &s.Shifts, &days, &s.LateAfterMinutes, &s.AbsenceLimitPercent,
			&s.Dre, &s.Ugel, &s.ModularCode, &s.Logo, &s.Management, &s.UpdatedAt)
	for _, d := range days {
		s.WeekDays = append(s.WeekDays, int(d))
	}
	if passing != nil {
		v := int(*passing)
		s.PassingGrade = &v
	}
	return s, academicErr(err)
}

func (r *AcademicRepository) SaveSettings(ctx context.Context, institutionID string, s domain.AcademicSettings) (domain.AcademicSettings, error) {
	if s.AttentionModes == nil {
		s.AttentionModes = []string{}
	}
	if len(s.WeekDays) == 0 {
		s.WeekDays = []int{1, 2, 3, 4, 5}
	}
	if s.AbsenceLimitPercent == 0 {
		s.LateAfterMinutes, s.AbsenceLimitPercent = 10, 30
	}
	// A profile built from scratch (first read, older callers) is private, as the column default
	if s.Management == "" {
		s.Management = domain.ManagementPrivate
	}
	_, err := r.c.pool.Exec(ctx, `INSERT INTO academic.settings (institution_id, term_scheme, grading_scale, passing_grade, conduct_grade, attention_modes, shifts, week_days, late_after_minutes, absence_limit_percent,
			dre, ugel, modular_code, logo, management)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::smallint[], $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (institution_id) DO UPDATE SET term_scheme = EXCLUDED.term_scheme, grading_scale = EXCLUDED.grading_scale,
			passing_grade = EXCLUDED.passing_grade, conduct_grade = EXCLUDED.conduct_grade, attention_modes = EXCLUDED.attention_modes,
			shifts = EXCLUDED.shifts, week_days = EXCLUDED.week_days, late_after_minutes = EXCLUDED.late_after_minutes,
			absence_limit_percent = EXCLUDED.absence_limit_percent, dre = EXCLUDED.dre, ugel = EXCLUDED.ugel,
			modular_code = EXCLUDED.modular_code, logo = EXCLUDED.logo, management = EXCLUDED.management`,
		institutionID, s.TermScheme, s.GradingScale, s.PassingGrade, s.ConductGrade, s.AttentionModes, s.Shifts, s.WeekDays, s.LateAfterMinutes, s.AbsenceLimitPercent,
		s.Dre, s.Ugel, s.ModularCode, s.Logo, s.Management)
	if err != nil {
		return domain.AcademicSettings{}, academicErr(err)
	}
	return r.Settings(ctx, institutionID)
}

// TuitionInUse is what stops an institution from turning public: the fee plans of periods not closed yet that
// still have tuition items, and how many tuition charges of those periods are still owed (paid ones are history)
func (r *AcademicRepository) TuitionInUse(ctx context.Context, institutionID string) ([]string, int, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT DISTINCT p.name FROM finance.fee_plans p
		JOIN finance.fee_items i ON i.plan_id = p.id AND i.concept = 'tuition'
		JOIN academic.periods pe ON pe.id = p.period_id AND pe.status <> 'closed'
		WHERE p.institution_id = $1 ORDER BY p.name`, institutionID)
	if err != nil {
		return nil, 0, academicErr(err)
	}
	plans, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, 0, academicErr(err)
	}
	var pending int
	err = r.c.pool.QueryRow(ctx, `SELECT count(*) FROM finance.charges ch
		JOIN academic.periods pe ON pe.id = ch.period_id AND pe.status <> 'closed'
		WHERE ch.institution_id = $1 AND ch.concept = 'tuition' AND ch.status = 'pending'
			AND ch.amount - ch.discount > coalesce((SELECT sum(a.amount) FROM finance.allocations a
				JOIN finance.payments pa ON pa.id = a.payment_id AND pa.status = 'valid' WHERE a.charge_id = ch.id), 0)`, institutionID).Scan(&pending)
	return plans, pending, academicErr(err)
}

// ---------- levels and grades ----------

const levelColumns = `l.id, l.institution_id, l.code, l.name, l.description, l.position, coalesce(l.formative_level, ''), l.credits,
	l.attention_modes, l.is_active,
	(SELECT count(*) FROM academic.sections s WHERE s.level_id = l.id)`

func scanLevel(row pgx.Row) (domain.Level, error) {
	var l domain.Level
	var credits *int16
	var position int16
	err := row.Scan(&l.ID, &l.InstitutionID, &l.Code, &l.Name, &l.Description, &position, &l.FormativeLevel, &credits, &l.AttentionModes, &l.IsActive, &l.SectionCount)
	l.Position = int(position)
	if credits != nil {
		v := int(*credits)
		l.Credits = &v
	}
	l.Grades = []domain.Grade{}
	return l, err
}

const gradeColumns = `g.id, g.level_id, g.code, g.name, g.short_name, g.position,
	(SELECT count(*) FROM academic.sections s WHERE s.grade_id = g.id)`

func scanGrade(row pgx.Row) (domain.Grade, error) {
	var g domain.Grade
	var position int16
	err := row.Scan(&g.ID, &g.LevelID, &g.Code, &g.Name, &g.ShortName, &position, &g.SectionCount)
	g.Position = int(position)
	return g, err
}

func (r *AcademicRepository) Levels(ctx context.Context, institutionID string) ([]domain.Level, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+levelColumns+` FROM academic.levels l WHERE l.institution_id = $1 ORDER BY l.position, l.name`, institutionID)
	if err != nil {
		return nil, err
	}
	levels, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Level, error) { return scanLevel(row) })
	if err != nil {
		return nil, err
	}
	gradeRows, err := r.c.pool.Query(ctx, `SELECT `+gradeColumns+` FROM academic.grades g
		JOIN academic.levels l ON l.id = g.level_id WHERE l.institution_id = $1 ORDER BY g.position, g.name`, institutionID)
	if err != nil {
		return nil, err
	}
	grades, err := pgx.CollectRows(gradeRows, func(row pgx.CollectableRow) (domain.Grade, error) { return scanGrade(row) })
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	for i, l := range levels {
		index[l.ID] = i
	}
	for _, g := range grades {
		if i, ok := index[g.LevelID]; ok {
			levels[i].Grades = append(levels[i].Grades, g)
		}
	}
	return levels, nil
}

func (r *AcademicRepository) Level(ctx context.Context, institutionID, levelID string) (domain.Level, error) {
	l, err := scanLevel(r.c.pool.QueryRow(ctx, `SELECT `+levelColumns+` FROM academic.levels l WHERE l.institution_id = $1 AND l.id = $2`, institutionID, levelID))
	if err != nil {
		return domain.Level{}, academicErr(err)
	}
	rows, err := r.c.pool.Query(ctx, `SELECT `+gradeColumns+` FROM academic.grades g WHERE g.level_id = $1 ORDER BY g.position, g.name`, levelID)
	if err != nil {
		return domain.Level{}, err
	}
	l.Grades, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Grade, error) { return scanGrade(row) })
	return l, err
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r *AcademicRepository) CreateLevel(ctx context.Context, institutionID string, in domain.LevelInput) (domain.Level, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO academic.levels (institution_id, code, name, description, position, formative_level, credits, attention_modes, is_active)
		VALUES ($1, $2, $3, $4, (SELECT coalesce(max(position) + 1, 0) FROM academic.levels WHERE institution_id = $1), $5, $6, $7, $8)
		RETURNING id`, institutionID, in.Code, in.Name, in.Description, nilIfEmpty(in.FormativeLevel), in.Credits, in.AttentionModes, in.IsActive).Scan(&id)
	if err != nil {
		return domain.Level{}, academicErr(err)
	}
	return r.Level(ctx, institutionID, id)
}

func (r *AcademicRepository) UpdateLevel(ctx context.Context, institutionID, levelID string, in domain.LevelInput) (domain.Level, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE academic.levels SET code = $3, name = $4, description = $5, formative_level = $6, credits = $7,
		attention_modes = $8, is_active = $9 WHERE institution_id = $1 AND id = $2`,
		institutionID, levelID, in.Code, in.Name, in.Description, nilIfEmpty(in.FormativeLevel), in.Credits, in.AttentionModes, in.IsActive)
	if err != nil {
		return domain.Level{}, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Level{}, domain.ErrNotFound
	}
	return r.Level(ctx, institutionID, levelID)
}

func (r *AcademicRepository) DeleteLevel(ctx context.Context, institutionID, levelID string) error {
	var grades, sections int
	err := r.c.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM academic.grades WHERE level_id = l.id), (SELECT count(*) FROM academic.sections WHERE level_id = l.id)
		FROM academic.levels l WHERE l.institution_id = $1 AND l.id = $2`, institutionID, levelID).Scan(&grades, &sections)
	if err != nil {
		return academicErr(err)
	}
	if grades > 0 || sections > 0 {
		return domain.ErrLevelInUse
	}
	_, err = r.c.pool.Exec(ctx, `DELETE FROM academic.levels WHERE institution_id = $1 AND id = $2`, institutionID, levelID)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23503" {
		return domain.ErrLevelInUse
	}
	return academicErr(err)
}

// reorder sets position = index for ids, which must be exactly the rows the scope query returns
func (r *AcademicRepository) reorder(ctx context.Context, scopeSQL, updateSQL string, scopeArgs []any, ids []string) error {
	return pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, scopeSQL, scopeArgs...)
		if err != nil {
			return err
		}
		current, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if len(current) != len(ids) {
			return domain.Validation(map[string]string{"ids": "La lista no coincide con la actual. Recarga la página."})
		}
		for _, id := range ids {
			if !slices.Contains(current, id) {
				return domain.Validation(map[string]string{"ids": "La lista no coincide con la actual. Recarga la página."})
			}
		}
		for i, id := range ids {
			if _, err := tx.Exec(ctx, updateSQL, id, i); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *AcademicRepository) ReorderLevels(ctx context.Context, institutionID string, ids []string) error {
	return academicErr(r.reorder(ctx, `SELECT id::text FROM academic.levels WHERE institution_id = $1`,
		`UPDATE academic.levels SET position = $2 WHERE id = $1`, []any{institutionID}, ids))
}

func (r *AcademicRepository) grade(ctx context.Context, institutionID, gradeID string) (domain.Grade, error) {
	g, err := scanGrade(r.c.pool.QueryRow(ctx, `SELECT `+gradeColumns+` FROM academic.grades g JOIN academic.levels l ON l.id = g.level_id
		WHERE l.institution_id = $1 AND g.id = $2`, institutionID, gradeID))
	return g, academicErr(err)
}

func (r *AcademicRepository) CreateGrade(ctx context.Context, institutionID, levelID string, in domain.GradeInput) (domain.Grade, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO academic.grades (level_id, code, name, short_name, position)
		SELECT l.id, $3, $4, $5, (SELECT coalesce(max(position) + 1, 0) FROM academic.grades WHERE level_id = l.id)
		FROM academic.levels l WHERE l.institution_id = $1 AND l.id = $2
		RETURNING id`, institutionID, levelID, in.Code, in.Name, in.ShortName).Scan(&id)
	if err != nil {
		return domain.Grade{}, academicErr(err)
	}
	return r.grade(ctx, institutionID, id)
}

func (r *AcademicRepository) UpdateGrade(ctx context.Context, institutionID, gradeID string, in domain.GradeInput) (domain.Grade, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE academic.grades g SET code = $3, name = $4, short_name = $5
		FROM academic.levels l WHERE l.id = g.level_id AND l.institution_id = $1 AND g.id = $2`, institutionID, gradeID, in.Code, in.Name, in.ShortName)
	if err != nil {
		return domain.Grade{}, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Grade{}, domain.ErrNotFound
	}
	return r.grade(ctx, institutionID, gradeID)
}

func (r *AcademicRepository) DeleteGrade(ctx context.Context, institutionID, gradeID string) error {
	g, err := r.grade(ctx, institutionID, gradeID)
	if err != nil {
		return err
	}
	if g.SectionCount > 0 {
		return domain.ErrGradeInUse
	}
	_, err = r.c.pool.Exec(ctx, `DELETE FROM academic.grades WHERE id = $1`, gradeID)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23503" {
		return domain.ErrGradeInUse
	}
	return academicErr(err)
}

func (r *AcademicRepository) ReorderGrades(ctx context.Context, institutionID, levelID string, ids []string) error {
	if _, err := r.Level(ctx, institutionID, levelID); err != nil {
		return err
	}
	return academicErr(r.reorder(ctx, `SELECT id::text FROM academic.grades WHERE level_id = $1`,
		`UPDATE academic.grades SET position = $2 WHERE id = $1`, []any{levelID}, ids))
}

func (r *AcademicRepository) CreateStructure(ctx context.Context, institutionID string, levels []domain.Level) error {
	return academicErr(pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		for _, l := range levels {
			modes := l.AttentionModes
			if modes == nil {
				modes = []string{}
			}
			var id string
			if err := tx.QueryRow(ctx, `INSERT INTO academic.levels (institution_id, code, name, description, position, formative_level, credits, attention_modes, is_active)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true) RETURNING id`,
				institutionID, l.Code, l.Name, l.Description, l.Position, nilIfEmpty(l.FormativeLevel), l.Credits, modes).Scan(&id); err != nil {
				return err
			}
			for _, g := range l.Grades {
				if _, err := tx.Exec(ctx, `INSERT INTO academic.grades (level_id, code, name, short_name, position) VALUES ($1, $2, $3, $4, $5)`,
					id, g.Code, g.Name, g.ShortName, g.Position); err != nil {
					return err
				}
			}
		}
		return nil
	}))
}

// ---------- periods and terms ----------

const periodColumns = `p.id, p.institution_id, p.kind, p.name, coalesce(p.season, ''), p.starts_on, p.ends_on, p.term_scheme, p.status,
	(SELECT count(*) FROM academic.sections s WHERE s.period_id = p.id),
	(SELECT coalesce(sum(s.capacity), 0) FROM academic.sections s WHERE s.period_id = p.id)`

func scanPeriod(row pgx.Row) (domain.Period, error) {
	var p domain.Period
	err := row.Scan(&p.ID, &p.InstitutionID, &p.Kind, &p.Name, &p.Season, &p.StartsOn, &p.EndsOn, &p.TermScheme, &p.Status, &p.SectionCount, &p.CapacityTotal)
	p.Terms = []domain.Term{}
	return p, err
}

func scanTerm(row pgx.Row) (domain.Term, error) {
	var t domain.Term
	var number int16
	err := row.Scan(&t.ID, &t.PeriodID, &number, &t.Name, &t.StartsOn, &t.EndsOn, &t.Status)
	t.Number = int(number)
	return t, err
}

const termColumns = `t.id, t.period_id, t.number, t.name, t.starts_on, t.ends_on, t.status`

func (r *AcademicRepository) Periods(ctx context.Context, institutionID string) ([]domain.Period, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+periodColumns+` FROM academic.periods p WHERE p.institution_id = $1 ORDER BY p.starts_on DESC, p.name`, institutionID)
	if err != nil {
		return nil, err
	}
	periods, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Period, error) { return scanPeriod(row) })
	if err != nil {
		return nil, err
	}
	termRows, err := r.c.pool.Query(ctx, `SELECT `+termColumns+` FROM academic.terms t JOIN academic.periods p ON p.id = t.period_id
		WHERE p.institution_id = $1 ORDER BY t.number`, institutionID)
	if err != nil {
		return nil, err
	}
	terms, err := pgx.CollectRows(termRows, func(row pgx.CollectableRow) (domain.Term, error) { return scanTerm(row) })
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	for i, p := range periods {
		index[p.ID] = i
	}
	for _, t := range terms {
		periods[index[t.PeriodID]].Terms = append(periods[index[t.PeriodID]].Terms, t)
	}
	return periods, nil
}

func (r *AcademicRepository) Period(ctx context.Context, institutionID, periodID string) (domain.Period, error) {
	p, err := scanPeriod(r.c.pool.QueryRow(ctx, `SELECT `+periodColumns+` FROM academic.periods p WHERE p.institution_id = $1 AND p.id = $2`, institutionID, periodID))
	if err != nil {
		return domain.Period{}, academicErr(err)
	}
	rows, err := r.c.pool.Query(ctx, `SELECT `+termColumns+` FROM academic.terms t WHERE t.period_id = $1 ORDER BY t.number`, periodID)
	if err != nil {
		return domain.Period{}, err
	}
	p.Terms, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Term, error) { return scanTerm(row) })
	return p, err
}

func insertTerms(ctx context.Context, tx pgx.Tx, periodID string, terms []domain.Term) error {
	for _, t := range terms {
		if _, err := tx.Exec(ctx, `INSERT INTO academic.terms (period_id, number, name, starts_on, ends_on, status) VALUES ($1, $2, $3, $4, $5, $6)`,
			periodID, t.Number, t.Name, date(t.StartsOn), date(t.EndsOn), t.Status); err != nil {
			return err
		}
	}
	return nil
}

func (r *AcademicRepository) CreatePeriod(ctx context.Context, institutionID string, p domain.Period) (domain.Period, error) {
	var id string
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO academic.periods (institution_id, kind, name, season, starts_on, ends_on, term_scheme, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			institutionID, p.Kind, p.Name, nilIfEmpty(p.Season), date(p.StartsOn), date(p.EndsOn), p.TermScheme, p.Status).Scan(&id); err != nil {
			return err
		}
		return insertTerms(ctx, tx, id, p.Terms)
	})
	if err != nil {
		return domain.Period{}, academicErr(err)
	}
	return r.Period(ctx, institutionID, id)
}

func (r *AcademicRepository) UpdatePeriod(ctx context.Context, institutionID string, p domain.Period, terms []domain.Term) (domain.Period, error) {
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE academic.periods SET name = $3, season = $4, starts_on = $5, ends_on = $6, term_scheme = $7
			WHERE institution_id = $1 AND id = $2`, institutionID, p.ID, p.Name, nilIfEmpty(p.Season), date(p.StartsOn), date(p.EndsOn), p.TermScheme)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		if terms == nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM academic.terms WHERE period_id = $1`, p.ID); err != nil {
			return err
		}
		return insertTerms(ctx, tx, p.ID, terms)
	})
	if err != nil {
		return domain.Period{}, academicErr(err)
	}
	return r.Period(ctx, institutionID, p.ID)
}

func (r *AcademicRepository) SetPeriodStatus(ctx context.Context, institutionID, periodID, status string) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE academic.periods SET status = $3 WHERE institution_id = $1 AND id = $2`, institutionID, periodID, status)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *AcademicRepository) DeletePeriod(ctx context.Context, institutionID, periodID string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM academic.periods WHERE institution_id = $1 AND id = $2`, institutionID, periodID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *AcademicRepository) UpdateTerm(ctx context.Context, institutionID, termID string, in domain.TermInput) (domain.Term, error) {
	t, err := scanTerm(r.c.pool.QueryRow(ctx, `UPDATE academic.terms t SET name = $3, starts_on = $4, ends_on = $5, status = $6
		FROM academic.periods p WHERE p.id = t.period_id AND p.institution_id = $1 AND t.id = $2
		RETURNING `+termColumns, institutionID, termID, in.Name, date(in.StartsOn), date(in.EndsOn), in.Status))
	return t, academicErr(err)
}

// ---------- sections ----------

const sectionColumns = `s.id, s.period_id, s.level_id, l.name, coalesce(s.grade_id::text, ''), coalesce(g.name, ''), coalesce(g.short_name, ''),
	s.name, s.shift, coalesce(s.attention_mode, ''), coalesce(s.program, ''), coalesce(s.distance_variant, ''), s.multigrade, s.capacity, s.room, l.position, coalesce(g.position, 0),
	coalesce(s.tutor_id::text, ''), coalesce((SELECT st.given_names || ' ' || st.surnames FROM people.staff st WHERE st.id = s.tutor_id), '')`

const sectionFrom = ` FROM academic.sections s
	JOIN academic.periods p ON p.id = s.period_id
	JOIN academic.levels l ON l.id = s.level_id
	LEFT JOIN academic.grades g ON g.id = s.grade_id`

func scanSection(row pgx.Row) (domain.Section, error) {
	var s domain.Section
	var capacity, levelPos, gradePos int16
	err := row.Scan(&s.ID, &s.PeriodID, &s.LevelID, &s.LevelName, &s.GradeID, &s.GradeName, &s.GradeShort, &s.Name, &s.Shift, &s.AttentionMode, &s.Program, &s.DistanceVariant, &s.Multigrade, &capacity, &s.Room, &levelPos, &gradePos, &s.TutorID, &s.TutorName)
	s.Capacity, s.LevelPosition, s.GradePosition = int(capacity), int(levelPos), int(gradePos)
	return s, err
}

func (r *AcademicRepository) Sections(ctx context.Context, institutionID, periodID string) ([]domain.Section, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+sectionColumns+sectionFrom+`
		WHERE p.institution_id = $1 AND s.period_id = $2
		ORDER BY l.position, g.position NULLS FIRST, s.name`, institutionID, periodID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Section, error) { return scanSection(row) })
	return out, academicErr(err)
}

func (r *AcademicRepository) Section(ctx context.Context, institutionID, sectionID string) (domain.Section, error) {
	s, err := scanSection(r.c.pool.QueryRow(ctx, `SELECT `+sectionColumns+sectionFrom+` WHERE p.institution_id = $1 AND s.id = $2`, institutionID, sectionID))
	return s, academicErr(err)
}

func (r *AcademicRepository) CreateSections(ctx context.Context, institutionID, periodID string, sections []domain.Section) ([]domain.Section, error) {
	var ids []string
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM academic.periods WHERE institution_id = $1 AND id = $2)`, institutionID, periodID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return domain.ErrNotFound
		}
		for _, s := range sections {
			var id string
			// The level (and grade) must belong to the same institution
			err := tx.QueryRow(ctx, `INSERT INTO academic.sections (period_id, level_id, grade_id, name, shift, attention_mode, capacity, room, program, distance_variant, multigrade)
				SELECT $1, l.id, $3::uuid, $4, $5, $6, $7, $8, $10, $11, $12 FROM academic.levels l
				WHERE l.id = $2 AND l.institution_id = $9
				  AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM academic.grades g WHERE g.id = $3::uuid AND g.level_id = l.id))
				RETURNING id`,
				periodID, s.LevelID, nilIfEmpty(s.GradeID), s.Name, s.Shift, nilIfEmpty(s.AttentionMode), s.Capacity, s.Room, institutionID,
				nilIfEmpty(s.Program), nilIfEmpty(s.DistanceVariant), s.Multigrade).Scan(&id)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, academicErr(err)
	}
	out := make([]domain.Section, 0, len(ids))
	for _, id := range ids {
		s, err := r.Section(ctx, institutionID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (r *AcademicRepository) OtherGrades(ctx context.Context, sectionID string) (int, error) {
	var n int
	err := r.c.pool.QueryRow(ctx, `SELECT count(*) FROM enrollment.enrollments e JOIN academic.sections s ON s.id = e.section_id
		WHERE e.section_id = $1 AND e.status = 'active' AND e.grade_id IS DISTINCT FROM s.grade_id`, sectionID).Scan(&n)
	return n, academicErr(err)
}

func (r *AcademicRepository) UpdateSection(ctx context.Context, institutionID, sectionID string, in domain.SectionInput) (domain.Section, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE academic.sections s SET name = $3, shift = $4, attention_mode = $5, capacity = $6, room = $7,
		program = $8, distance_variant = $9, multigrade = $10
		FROM academic.periods p WHERE p.id = s.period_id AND p.institution_id = $1 AND s.id = $2`,
		institutionID, sectionID, in.Name, in.Shift, nilIfEmpty(in.AttentionMode), in.Capacity, in.Room, nilIfEmpty(in.Program), nilIfEmpty(in.DistanceVariant), in.Multigrade)
	if err != nil {
		return domain.Section{}, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Section{}, domain.ErrNotFound
	}
	return r.Section(ctx, institutionID, sectionID)
}

func (r *AcademicRepository) AuthorizedModes(ctx context.Context, institutionID string, day time.Time) (map[string][]string, bool, error) {
	return authorizedModes(ctx, r.c, institutionID, day)
}

func (r *AcademicRepository) DeleteSection(ctx context.Context, institutionID, sectionID string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM academic.sections s USING academic.periods p
		WHERE p.id = s.period_id AND p.institution_id = $1 AND s.id = $2`, institutionID, sectionID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

// providerOrMeet stores where a live session happens; empty (older clients) keeps the external link
func providerOrMeet(p string) string {
	if p == "" {
		return "meet"
	}
	return p
}
