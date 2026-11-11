package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// CurriculumRepository implements port.CurriculumRepository over the study plan tables of the academic schema
type CurriculumRepository struct {
	c *Client
}

func NewCurriculumRepository(c *Client) *CurriculumRepository {
	return &CurriculumRepository{c: c}
}

var _ port.CurriculumRepository = (*CurriculumRepository)(nil)

func smallPtr(v *int16) *int {
	if v == nil {
		return nil
	}
	x := int(*v)
	return &x
}

// ---------- plans ----------

const curriculumColumns = `c.id, c.institution_id, c.level_id, l.name, c.name, c.notes, c.status, c.activated_at, c.updated_at,
	(SELECT count(*) FROM academic.subjects s WHERE s.curriculum_id = c.id),
	(SELECT coalesce(sum(coalesce(s.theory_credits, 0) + coalesce(s.practice_credits, 0)), 0) FROM academic.subjects s WHERE s.curriculum_id = c.id)`

func scanCurriculum(row pgx.Row) (domain.Curriculum, error) {
	var c domain.Curriculum
	err := row.Scan(&c.ID, &c.InstitutionID, &c.LevelID, &c.LevelName, &c.Name, &c.Notes, &c.Status, &c.ActivatedAt, &c.UpdatedAt, &c.SubjectCount, &c.TotalCredits)
	c.Modules, c.Subjects, c.Transversal = []domain.Module{}, []domain.Subject{}, []domain.Competency{}
	return c, err
}

func (r *CurriculumRepository) Curricula(ctx context.Context, institutionID string) ([]domain.Curriculum, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+curriculumColumns+` FROM academic.curricula c JOIN academic.levels l ON l.id = c.level_id
		WHERE c.institution_id = $1 ORDER BY l.position, c.created_at DESC`, institutionID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Curriculum, error) { return scanCurriculum(row) })
}

func (r *CurriculumRepository) Curriculum(ctx context.Context, institutionID, id string) (domain.Curriculum, error) {
	c, err := scanCurriculum(r.c.pool.QueryRow(ctx, `SELECT `+curriculumColumns+` FROM academic.curricula c JOIN academic.levels l ON l.id = c.level_id
		WHERE c.institution_id = $1 AND c.id = $2`, institutionID, id))
	if err != nil {
		return domain.Curriculum{}, academicErr(err)
	}

	moduleRows, err := r.c.pool.Query(ctx, `SELECT id, code, name, description, position FROM academic.modules WHERE curriculum_id = $1 ORDER BY position, name`, id)
	if err != nil {
		return domain.Curriculum{}, err
	}
	c.Modules, err = pgx.CollectRows(moduleRows, func(row pgx.CollectableRow) (domain.Module, error) {
		var m domain.Module
		var pos int16
		err := row.Scan(&m.ID, &m.Code, &m.Name, &m.Description, &pos)
		m.Position = int(pos)
		return m, err
	})
	if err != nil {
		return domain.Curriculum{}, err
	}

	subjectRows, err := r.c.pool.Query(ctx, `SELECT s.id, coalesce(s.module_id::text, ''), s.kind, coalesce(s.component, ''), s.code, s.name, s.description,
			s.weekly_hours, s.theory_credits, s.practice_credits, s.position,
			coalesce((SELECT array_agg(sg.grade_id::text ORDER BY g.position) FROM academic.subject_grades sg JOIN academic.grades g ON g.id = sg.grade_id WHERE sg.subject_id = s.id), '{}')
		FROM academic.subjects s WHERE s.curriculum_id = $1 ORDER BY s.position, s.name`, id)
	if err != nil {
		return domain.Curriculum{}, err
	}
	c.Subjects, err = pgx.CollectRows(subjectRows, func(row pgx.CollectableRow) (domain.Subject, error) {
		var s domain.Subject
		var hours, theory, practice *int16
		var pos int16
		err := row.Scan(&s.ID, &s.ModuleID, &s.Kind, &s.Component, &s.Code, &s.Name, &s.Description, &hours, &theory, &practice, &pos, &s.GradeIDs)
		s.CurriculumID, s.Position = id, int(pos)
		s.WeeklyHours, s.TheoryCredits, s.PracticeCredits = smallPtr(hours), smallPtr(theory), smallPtr(practice)
		s.Competencies = []domain.Competency{}
		return s, err
	})
	if err != nil {
		return domain.Curriculum{}, err
	}

	compRows, err := r.c.pool.Query(ctx, `SELECT id, coalesce(subject_id::text, ''), kind, code, name, position FROM academic.competencies
		WHERE curriculum_id = $1 ORDER BY position, name`, id)
	if err != nil {
		return domain.Curriculum{}, err
	}
	comps, err := pgx.CollectRows(compRows, func(row pgx.CollectableRow) (domain.Competency, error) { return scanCompetency(row) })
	if err != nil {
		return domain.Curriculum{}, err
	}
	index := map[string]int{}
	for i, s := range c.Subjects {
		index[s.ID] = i
	}
	for _, comp := range comps {
		if comp.SubjectID == "" {
			c.Transversal = append(c.Transversal, comp)
		} else if i, ok := index[comp.SubjectID]; ok {
			c.Subjects[i].Competencies = append(c.Subjects[i].Competencies, comp)
		}
	}
	return c, nil
}

func scanCompetency(row pgx.Row) (domain.Competency, error) {
	var comp domain.Competency
	var pos int16
	err := row.Scan(&comp.ID, &comp.SubjectID, &comp.Kind, &comp.Code, &comp.Name, &pos)
	comp.Position = int(pos)
	return comp, err
}

// kindOfCompetencies is what an institution assesses, read from its kind
const competencyKindSQL = `CASE (SELECT kind FROM iam.institutions WHERE id = $1) WHEN 'cepre' THEN 'topic' WHEN 'institute' THEN 'capacity' ELSE 'competency' END`

func (r *CurriculumRepository) CreateCurriculum(ctx context.Context, institutionID string, c domain.Curriculum) (domain.Curriculum, error) {
	var id string
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO academic.curricula (institution_id, level_id, name, notes, status)
			SELECT $1, l.id, $3, $4, $5 FROM academic.levels l WHERE l.id = $2 AND l.institution_id = $1
			RETURNING id`, institutionID, c.LevelID, c.Name, c.Notes, c.Status).Scan(&id); err != nil {
			return err
		}
		moduleID := map[string]string{}
		for i, m := range c.Modules {
			var mid string
			if err := tx.QueryRow(ctx, `INSERT INTO academic.modules (curriculum_id, code, name, description, position) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
				id, m.Code, m.Name, m.Description, i).Scan(&mid); err != nil {
				return err
			}
			moduleID[m.Code] = mid
		}
		for i, s := range c.Subjects {
			var sid string
			if err := tx.QueryRow(ctx, `INSERT INTO academic.subjects (curriculum_id, module_id, kind, component, code, name, description, weekly_hours, theory_credits, practice_credits, position)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
				id, nilIfEmpty(moduleID[s.ModuleID]), s.Kind, nilIfEmpty(s.Component), s.Code, s.Name, s.Description, s.WeeklyHours, s.TheoryCredits, s.PracticeCredits, i).Scan(&sid); err != nil {
				return err
			}
			if err := insertSubjectGrades(ctx, tx, institutionID, sid, s.GradeIDs); err != nil {
				return err
			}
			for j, comp := range s.Competencies {
				if _, err := tx.Exec(ctx, `INSERT INTO academic.competencies (curriculum_id, subject_id, kind, code, name, position) VALUES ($2, $3, `+competencyKindSQL+`, $4, $5, $6)`,
					institutionID, id, sid, comp.Code, comp.Name, j); err != nil {
					return err
				}
			}
		}
		for j, comp := range c.Transversal {
			if _, err := tx.Exec(ctx, `INSERT INTO academic.competencies (curriculum_id, subject_id, kind, code, name, position) VALUES ($2, NULL, `+competencyKindSQL+`, $3, $4, $5)`,
				institutionID, id, comp.Code, comp.Name, j); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Curriculum{}, academicErr(err)
	}
	return r.Curriculum(ctx, institutionID, id)
}

// insertSubjectGrades links the subject to grades of the institution only
func insertSubjectGrades(ctx context.Context, tx pgx.Tx, institutionID, subjectID string, gradeIDs []string) error {
	if len(gradeIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO academic.subject_grades (subject_id, grade_id)
		SELECT $2, g.id FROM academic.grades g JOIN academic.levels l ON l.id = g.level_id
		WHERE l.institution_id = $1 AND g.id = ANY ($3::uuid[])`, institutionID, subjectID, gradeIDs)
	return err
}

func (r *CurriculumRepository) UpdateCurriculum(ctx context.Context, institutionID, id string, in domain.CurriculumInput) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE academic.curricula SET name = $3, notes = $4 WHERE institution_id = $1 AND id = $2`, institutionID, id, in.Name, in.Notes)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *CurriculumRepository) SetCurriculumStatus(ctx context.Context, institutionID, id, status string) error {
	return academicErr(pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		if status == domain.CurriculumActive {
			if _, err := tx.Exec(ctx, `UPDATE academic.curricula SET status = 'archived'
				WHERE status = 'active' AND id <> $2 AND level_id = (SELECT level_id FROM academic.curricula WHERE institution_id = $1 AND id = $2)`, institutionID, id); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE academic.curricula SET status = $3::text, activated_at = CASE WHEN $3::text = 'active' THEN $4 ELSE activated_at END
			WHERE institution_id = $1 AND id = $2`, institutionID, id, status, time.Now())
		if err == nil && tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return err
	}))
}

func (r *CurriculumRepository) DeleteCurriculum(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM academic.curricula WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

// ---------- ownership lookups ----------

func (r *CurriculumRepository) lookup(ctx context.Context, sql, institutionID, id string) (string, error) {
	var curriculumID string
	err := r.c.pool.QueryRow(ctx, sql, institutionID, id).Scan(&curriculumID)
	return curriculumID, academicErr(err)
}

func (r *CurriculumRepository) SubjectCurriculum(ctx context.Context, institutionID, subjectID string) (string, error) {
	return r.lookup(ctx, `SELECT c.id FROM academic.subjects s JOIN academic.curricula c ON c.id = s.curriculum_id WHERE c.institution_id = $1 AND s.id = $2`, institutionID, subjectID)
}

func (r *CurriculumRepository) ModuleCurriculum(ctx context.Context, institutionID, moduleID string) (string, error) {
	return r.lookup(ctx, `SELECT c.id FROM academic.modules m JOIN academic.curricula c ON c.id = m.curriculum_id WHERE c.institution_id = $1 AND m.id = $2`, institutionID, moduleID)
}

func (r *CurriculumRepository) CompetencyCurriculum(ctx context.Context, institutionID, competencyID string) (string, error) {
	return r.lookup(ctx, `SELECT c.id FROM academic.competencies x JOIN academic.curricula c ON c.id = x.curriculum_id WHERE c.institution_id = $1 AND x.id = $2`, institutionID, competencyID)
}

// ---------- modules ----------

func (r *CurriculumRepository) module(ctx context.Context, id string) (domain.Module, error) {
	var m domain.Module
	var pos int16
	err := r.c.pool.QueryRow(ctx, `SELECT id, code, name, description, position FROM academic.modules WHERE id = $1`, id).Scan(&m.ID, &m.Code, &m.Name, &m.Description, &pos)
	m.Position = int(pos)
	return m, academicErr(err)
}

func (r *CurriculumRepository) CreateModule(ctx context.Context, institutionID, curriculumID string, in domain.ModuleInput) (domain.Module, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO academic.modules (curriculum_id, code, name, description, position)
		SELECT c.id, $3, $4, $5, (SELECT coalesce(max(position) + 1, 0) FROM academic.modules WHERE curriculum_id = c.id)
		FROM academic.curricula c WHERE c.institution_id = $1 AND c.id = $2 RETURNING id`, institutionID, curriculumID, in.Code, in.Name, in.Description).Scan(&id)
	if err != nil {
		return domain.Module{}, academicErr(err)
	}
	return r.module(ctx, id)
}

func (r *CurriculumRepository) UpdateModule(ctx context.Context, institutionID, id string, in domain.ModuleInput) (domain.Module, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE academic.modules m SET code = $3, name = $4, description = $5
		FROM academic.curricula c WHERE c.id = m.curriculum_id AND c.institution_id = $1 AND m.id = $2`, institutionID, id, in.Code, in.Name, in.Description)
	if err != nil {
		return domain.Module{}, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Module{}, domain.ErrNotFound
	}
	return r.module(ctx, id)
}

func (r *CurriculumRepository) DeleteModule(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM academic.modules m USING academic.curricula c WHERE c.id = m.curriculum_id AND c.institution_id = $1 AND m.id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

// ---------- subjects ----------

func (r *CurriculumRepository) subject(ctx context.Context, institutionID, id string) (domain.Subject, error) {
	curriculumID, err := r.SubjectCurriculum(ctx, institutionID, id)
	if err != nil {
		return domain.Subject{}, err
	}
	c, err := r.Curriculum(ctx, institutionID, curriculumID)
	if err != nil {
		return domain.Subject{}, err
	}
	for _, s := range c.Subjects {
		if s.ID == id {
			return s, nil
		}
	}
	return domain.Subject{}, domain.ErrNotFound
}

func (r *CurriculumRepository) CreateSubject(ctx context.Context, institutionID, curriculumID string, in domain.SubjectInput) (domain.Subject, error) {
	var id string
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO academic.subjects (curriculum_id, module_id, kind, component, code, name, description, weekly_hours, theory_credits, practice_credits, position)
			SELECT c.id, $3, $4, $5, $6, $7, $8, $9, $10, $11, (SELECT coalesce(max(position) + 1, 0) FROM academic.subjects WHERE curriculum_id = c.id)
			FROM academic.curricula c WHERE c.institution_id = $1 AND c.id = $2 RETURNING id`,
			institutionID, curriculumID, nilIfEmpty(in.ModuleID), in.Kind, nilIfEmpty(in.Component), in.Code, in.Name, in.Description, in.WeeklyHours, in.TheoryCredits, in.PracticeCredits).Scan(&id); err != nil {
			return err
		}
		return insertSubjectGrades(ctx, tx, institutionID, id, in.GradeIDs)
	})
	if err != nil {
		return domain.Subject{}, academicErr(err)
	}
	return r.subject(ctx, institutionID, id)
}

func (r *CurriculumRepository) UpdateSubject(ctx context.Context, institutionID, id string, in domain.SubjectInput) (domain.Subject, error) {
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE academic.subjects s SET module_id = $3, kind = $4, component = $5, code = $6, name = $7, description = $8,
				weekly_hours = $9, theory_credits = $10, practice_credits = $11
			FROM academic.curricula c WHERE c.id = s.curriculum_id AND c.institution_id = $1 AND s.id = $2`,
			institutionID, id, nilIfEmpty(in.ModuleID), in.Kind, nilIfEmpty(in.Component), in.Code, in.Name, in.Description, in.WeeklyHours, in.TheoryCredits, in.PracticeCredits)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM academic.subject_grades WHERE subject_id = $1`, id); err != nil {
			return err
		}
		return insertSubjectGrades(ctx, tx, institutionID, id, in.GradeIDs)
	})
	if err != nil {
		return domain.Subject{}, academicErr(err)
	}
	return r.subject(ctx, institutionID, id)
}

func (r *CurriculumRepository) DeleteSubject(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM academic.subjects s USING academic.curricula c WHERE c.id = s.curriculum_id AND c.institution_id = $1 AND s.id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *CurriculumRepository) ReorderSubjects(ctx context.Context, institutionID, curriculumID string, ids []string) error {
	if _, err := r.lookup(ctx, `SELECT id FROM academic.curricula WHERE institution_id = $1 AND id = $2`, institutionID, curriculumID); err != nil {
		return err
	}
	academic := &AcademicRepository{c: r.c}
	return academicErr(academic.reorder(ctx, `SELECT id::text FROM academic.subjects WHERE curriculum_id = $1`,
		`UPDATE academic.subjects SET position = $2 WHERE id = $1`, []any{curriculumID}, ids))
}

// ---------- competencies ----------

func (r *CurriculumRepository) CreateCompetency(ctx context.Context, institutionID, curriculumID, kind string, in domain.CompetencyInput) (domain.Competency, error) {
	comp, err := scanCompetency(r.c.pool.QueryRow(ctx, `INSERT INTO academic.competencies (curriculum_id, subject_id, kind, code, name, position)
		SELECT c.id, $3, $4, $5, $6, (SELECT coalesce(max(position) + 1, 0) FROM academic.competencies WHERE curriculum_id = c.id AND subject_id IS NOT DISTINCT FROM $3::uuid)
		FROM academic.curricula c WHERE c.institution_id = $1 AND c.id = $2
		RETURNING id, coalesce(subject_id::text, ''), kind, code, name, position`,
		institutionID, curriculumID, nilIfEmpty(in.SubjectID), kind, in.Code, in.Name))
	return comp, academicErr(err)
}

func (r *CurriculumRepository) UpdateCompetency(ctx context.Context, institutionID, id string, in domain.CompetencyInput) (domain.Competency, error) {
	comp, err := scanCompetency(r.c.pool.QueryRow(ctx, `UPDATE academic.competencies x SET code = $3, name = $4
		FROM academic.curricula c WHERE c.id = x.curriculum_id AND c.institution_id = $1 AND x.id = $2
		RETURNING x.id, coalesce(x.subject_id::text, ''), x.kind, x.code, x.name, x.position`, institutionID, id, in.Code, in.Name))
	return comp, academicErr(err)
}

func (r *CurriculumRepository) DeleteCompetency(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM academic.competencies x USING academic.curricula c WHERE c.id = x.curriculum_id AND c.institution_id = $1 AND x.id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *CurriculumRepository) ReorderCompetencies(ctx context.Context, institutionID, curriculumID, subjectID string, ids []string) error {
	if _, err := r.lookup(ctx, `SELECT id FROM academic.curricula WHERE institution_id = $1 AND id = $2`, institutionID, curriculumID); err != nil {
		return err
	}
	academic := &AcademicRepository{c: r.c}
	return academicErr(academic.reorder(ctx, `SELECT id::text FROM academic.competencies WHERE curriculum_id = $1 AND subject_id IS NOT DISTINCT FROM $2::uuid`,
		`UPDATE academic.competencies SET position = $2 WHERE id = $1`, []any{curriculumID, nilIfEmpty(subjectID)}, ids))
}
