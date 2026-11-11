package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"api/internal/core/domain"
)

// Runs against a migrated database only: JOSEPH_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func TestAcademicStructureLifecycle(t *testing.T) {
	dsn := os.Getenv("JOSEPH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOSEPH_TEST_DATABASE_URL not set")
	}
	c, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx := context.Background()
	exec := func(sql string) {
		if _, err := c.pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`DELETE FROM iam.institutions WHERE slug = 'repo-academic'`)
	t.Cleanup(func() { exec(`DELETE FROM iam.institutions WHERE slug = 'repo-academic'`) })
	exec(`INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-academic', 'Repo Academic', 'ebr')`)
	repo := NewAcademicRepository(c)
	inst, err := repo.InstitutionBySlug(ctx, "repo-academic")
	if err != nil {
		t.Fatal(err)
	}

	err = repo.CreateStructure(ctx, inst.ID, []domain.Level{{Code: "primaria", Name: "Primaria", Grades: []domain.Grade{
		{Code: "1", Name: "1.° grado", ShortName: "1.°", Position: 0}, {Code: "2", Name: "2.° grado", ShortName: "2.°", Position: 1},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	levels, err := repo.Levels(ctx, inst.ID)
	if err != nil || len(levels) != 1 || len(levels[0].Grades) != 2 {
		t.Fatalf("levels = %+v, %v", levels, err)
	}
	level := levels[0]

	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	p, err := repo.CreatePeriod(ctx, inst.ID, domain.Period{Kind: domain.PeriodSchoolYear, Name: "Año lectivo 2030", StartsOn: day("2030-03-04"), EndsOn: day("2030-12-13"), TermScheme: domain.TermSchemeNone, Status: domain.PeriodPlanning, Terms: []domain.Term{}})
	if err != nil {
		t.Fatal(err)
	}
	sections, err := repo.CreateSections(ctx, inst.ID, p.ID, []domain.Section{{LevelID: level.ID, GradeID: level.Grades[0].ID, Name: "A", Shift: "morning", Capacity: 30}})
	if err != nil || len(sections) != 1 || sections[0].GradeShort != "1.°" {
		t.Fatalf("sections = %+v, %v", sections, err)
	}
	if _, err := repo.CreateSections(ctx, inst.ID, p.ID, []domain.Section{{LevelID: level.ID, GradeID: level.Grades[0].ID, Name: "A", Shift: "morning", Capacity: 30}}); !errors.Is(err, domain.ErrDuplicateName) {
		t.Fatalf("duplicate section: %v", err)
	}

	if err := repo.DeleteGrade(ctx, inst.ID, level.Grades[0].ID); !errors.Is(err, domain.ErrGradeInUse) {
		t.Fatalf("grade with sections: %v", err)
	}
	if err := repo.DeleteLevel(ctx, inst.ID, level.ID); !errors.Is(err, domain.ErrLevelInUse) {
		t.Fatalf("level with grades: %v", err)
	}
	if err := repo.DeleteGrade(ctx, inst.ID, level.Grades[1].ID); err != nil {
		t.Fatalf("free grade: %v", err)
	}

	// The institution goes with everything it has
	exec(`DELETE FROM iam.institutions WHERE slug = 'repo-academic'`)
	var left int
	if err := c.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM academic.levels WHERE id = $1) + (SELECT count(*) FROM academic.periods WHERE id = $2) + (SELECT count(*) FROM academic.sections WHERE period_id = $2)`, level.ID, p.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("rows left after deleting the institution: %d, %v", left, err)
	}
}
