package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
)

// EBA enrollment (phase E1): enrollment files, religion exemptions, SIAGIE states, the nómina and the actas of the
// placement procedures

func (r *PeopleRepository) EnrollmentFile(ctx context.Context, institutionID, studentID string) (domain.EnrollmentFile, error) {
	f := domain.EnrollmentFile{StudentID: studentID, MotherTongue: "castellano", Internet: "unknown", Device: "unknown", Documents: []string{}}
	var ok bool
	if err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people.students WHERE institution_id = $1 AND id = $2)`, institutionID, studentID).Scan(&ok); err != nil {
		return f, academicErr(err)
	}
	if !ok {
		return f, domain.ErrNotFound
	}
	var updated time.Time
	err := r.c.pool.QueryRow(ctx, `SELECT mother_tongue, mother_tongue_other, occupation, disability, internet, device, coalesce(preferred_mode, ''),
		last_studies, documents, updated_by_name, updated_at FROM people.enrollment_files WHERE student_id = $1`, studentID).
		Scan(&f.MotherTongue, &f.MotherTongueOther, &f.Occupation, &f.Disability, &f.Internet, &f.Device, &f.PreferredMode, &f.LastStudies, &f.Documents, &f.UpdatedByName, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, nil
	}
	if err != nil {
		return f, academicErr(err)
	}
	f.UpdatedAt = &updated
	return f, nil
}

func (r *PeopleRepository) SaveEnrollmentFile(ctx context.Context, institutionID string, f domain.EnrollmentFile) error {
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO people.enrollment_files (student_id, mother_tongue, mother_tongue_other, occupation, disability, internet, device,
		preferred_mode, last_studies, documents, updated_by_name)
		SELECT s.id, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12 FROM people.students s WHERE s.institution_id = $1 AND s.id = $2
		ON CONFLICT (student_id) DO UPDATE SET mother_tongue = EXCLUDED.mother_tongue, mother_tongue_other = EXCLUDED.mother_tongue_other,
		occupation = EXCLUDED.occupation, disability = EXCLUDED.disability, internet = EXCLUDED.internet, device = EXCLUDED.device,
		preferred_mode = EXCLUDED.preferred_mode, last_studies = EXCLUDED.last_studies, documents = EXCLUDED.documents, updated_by_name = EXCLUDED.updated_by_name`,
		institutionID, f.StudentID, f.MotherTongue, f.MotherTongueOther, f.Occupation, f.Disability, f.Internet, f.Device, nilIfEmpty(f.PreferredMode), f.LastStudies,
		f.Documents, f.UpdatedByName)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *PeopleRepository) SetReligionExemption(ctx context.Context, institutionID string, x domain.ReligionExemption) error {
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO enrollment.religion_exemptions (enrollment_id, requested_on, reference, granted_by_name)
		SELECT e.id, $3, $4, $5 FROM enrollment.enrollments e WHERE e.institution_id = $1 AND e.id = $2
		ON CONFLICT (enrollment_id) DO UPDATE SET requested_on = EXCLUDED.requested_on, reference = EXCLUDED.reference, granted_by_name = EXCLUDED.granted_by_name`,
		institutionID, x.EnrollmentID, date(x.RequestedOn), x.Reference, x.GrantedByName)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *PeopleRepository) RemoveReligionExemption(ctx context.Context, institutionID, enrollmentID string) error {
	_, err := r.c.pool.Exec(ctx, `DELETE FROM enrollment.religion_exemptions x USING enrollment.enrollments e
		WHERE e.id = x.enrollment_id AND e.institution_id = $1 AND x.enrollment_id = $2`, institutionID, enrollmentID)
	return academicErr(err)
}

// HasReligion reports whether the plan in force of the enrollment's level has the Educación Religiosa area
func (r *PeopleRepository) HasReligion(ctx context.Context, institutionID, enrollmentID string) (bool, error) {
	var ok bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM enrollment.enrollments e
		JOIN academic.curricula c ON c.level_id = e.level_id AND c.status = 'active'
		JOIN academic.subjects sj ON sj.curriculum_id = c.id AND sj.code = $3
		WHERE e.institution_id = $1 AND e.id = $2)`, institutionID, enrollmentID, domain.ReligionSubjectCode).Scan(&ok)
	return ok, academicErr(err)
}

func (r *PeopleRepository) Roll(ctx context.Context, institutionID string, f domain.RollFilter) ([]domain.RollRow, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT e.id, s.id, s.code, s.document_type, s.document_number, s.paternal_surname, s.maternal_surname, s.given_names,
		coalesce(s.sex, ''), s.birth_date, s.special_needs, e.level_id, l.code, l.name, coalesce(e.grade_id::text, ''), coalesce(g.name, ''), coalesce(g.position, 0),
		coalesce(e.section_id::text, ''), coalesce(sec.name, ''), coalesce(sec.attention_mode, ''), coalesce(sec.program, ''), e.kind, e.status, e.enrolled_on, e.ended_on,
		x.enrollment_id IS NOT NULL, coalesce(sr.status, 'pending'), coalesce(sr.note, ''), sr.exported_at, coalesce(ef.mother_tongue, ''),
		coalesce((SELECT CASE pl.kind WHEN 'placement_test' THEN 'Prueba de ubicación' WHEN 'validation' THEN 'Convalidación' WHEN 'revalidation' THEN 'Revalidación' ELSE 'Equivalencia' END
			|| CASE WHEN pl.act_number IS NOT NULL THEN ', acta N.° ' || lpad(pl.act_number::text, 3, '0') || '-' || pl.act_year ELSE '' END
			FROM enrollment.placements pl WHERE pl.student_id = s.id AND pl.level_id = e.level_id AND pl.grade_id = e.grade_id ORDER BY pl.decided_on DESC LIMIT 1), '')
		FROM enrollment.enrollments e
		JOIN people.students s ON s.id = e.student_id
		JOIN academic.levels l ON l.id = e.level_id
		LEFT JOIN academic.grades g ON g.id = e.grade_id
		LEFT JOIN academic.sections sec ON sec.id = e.section_id
		LEFT JOIN enrollment.religion_exemptions x ON x.enrollment_id = e.id
		LEFT JOIN enrollment.siagie_records sr ON sr.enrollment_id = e.id
		LEFT JOIN people.enrollment_files ef ON ef.student_id = s.id
		WHERE e.institution_id = $1 AND e.period_id = $2
		  AND ($3::uuid IS NULL OR e.level_id = $3::uuid) AND ($4::uuid IS NULL OR e.grade_id = $4::uuid) AND ($5::uuid IS NULL OR e.section_id = $5::uuid)
		  AND ($6 = '' OR coalesce(sr.status, 'pending') = $6) AND ($7 OR e.status = 'active')
		ORDER BY l.position, g.position NULLS FIRST, sec.name NULLS LAST, s.paternal_surname, s.maternal_surname, s.given_names`,
		institutionID, f.PeriodID, uuidOrNil(f.LevelID), uuidOrNil(f.GradeID), uuidOrNil(f.SectionID), f.SiagieStatus, f.All)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.RollRow, error) {
		var x domain.RollRow
		var pos int16
		err := row.Scan(&x.EnrollmentID, &x.StudentID, &x.StudentCode, &x.DocumentType, &x.DocumentNumber, &x.PaternalSurname, &x.MaternalSurname, &x.GivenNames,
			&x.Sex, &x.BirthDate, &x.SpecialNeeds, &x.LevelID, &x.LevelCode, &x.LevelName, &x.GradeID, &x.GradeName, &pos, &x.SectionID, &x.SectionName,
			&x.AttentionMode, &x.Program, &x.Kind, &x.Status, &x.EnrolledOn, &x.EndedOn, &x.ReligionExempt, &x.SiagieStatus, &x.SiagieNote, &x.SiagieExported,
			&x.MotherTongue, &x.Placement)
		x.GradePosition = int(pos)
		return x, err
	})
	if out == nil {
		out = []domain.RollRow{}
	}
	return out, academicErr(err)
}

func (r *PeopleRepository) SetSiagie(ctx context.Context, institutionID string, enrollmentIDs []string, status, note, by string, at time.Time) (int, error) {
	var exported *time.Time
	if status == domain.SiagieExported {
		exported = &at
	}
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO enrollment.siagie_records (enrollment_id, status, note, exported_at, updated_by_name)
		SELECT e.id, $3, $4, $5, $6 FROM enrollment.enrollments e WHERE e.institution_id = $1 AND e.id = ANY($2::uuid[])
		ON CONFLICT (enrollment_id) DO UPDATE SET status = EXCLUDED.status, note = EXCLUDED.note,
		exported_at = coalesce(EXCLUDED.exported_at, enrollment.siagie_records.exported_at), updated_by_name = EXCLUDED.updated_by_name`,
		institutionID, enrollmentIDs, status, note, exported, by)
	return int(tag.RowsAffected()), academicErr(err)
}

// AssignAct gives the placement the next acta number of its year, once; returns the year and number it keeps
func (r *PeopleRepository) AssignAct(ctx context.Context, institutionID, placementID string, year int) (int, int, error) {
	var y, n int
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		// One numbering per institution and year: serialize the assignments of the institution
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('placement-acts:' || $1::text))`, institutionID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `UPDATE enrollment.placements pl SET act_year = coalesce(pl.act_year, $3),
			act_number = coalesce(pl.act_number, (SELECT coalesce(max(o.act_number), 0) + 1 FROM enrollment.placements o WHERE o.institution_id = $1 AND o.act_year = $3))
			WHERE pl.institution_id = $1 AND pl.id = $2 RETURNING pl.act_year, pl.act_number`, institutionID, placementID, year).Scan(&y, &n)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, domain.ErrNotFound
	}
	return y, n, academicErr(err)
}

func (r *PeopleRepository) LinkAct(ctx context.Context, placementID, documentID string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE enrollment.placements SET document_id = $2 WHERE id = $1`, placementID, documentID)
	return academicErr(err)
}
