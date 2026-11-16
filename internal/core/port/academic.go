package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// AcademicRepository is academic.* in PostgreSQL. Every method that takes institutionID only touches rows of that
// institution: an id of another institution behaves as missing (domain.ErrNotFound). Unique violations come back
// as domain.ErrDuplicateName
type AcademicRepository interface {
	InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error)

	// Settings returns the profile, or domain.ErrNotFound when the institution has none yet
	Settings(ctx context.Context, institutionID string) (domain.AcademicSettings, error)
	SaveSettings(ctx context.Context, institutionID string, s domain.AcademicSettings) (domain.AcademicSettings, error)
	// TuitionInUse lists the fee plans of periods not closed that still have tuition items and counts the tuition
	// charges of those periods still owed: what must be cleared before the institution turns public
	TuitionInUse(ctx context.Context, institutionID string) (plans []string, pendingCharges int, err error)

	// Levels returns every level with its grades and section counts, in position order
	Levels(ctx context.Context, institutionID string) ([]domain.Level, error)
	Level(ctx context.Context, institutionID, levelID string) (domain.Level, error)
	CreateLevel(ctx context.Context, institutionID string, in domain.LevelInput) (domain.Level, error)
	UpdateLevel(ctx context.Context, institutionID, levelID string, in domain.LevelInput) (domain.Level, error)
	// DeleteLevel fails with domain.ErrLevelInUse while it has grades or sections
	DeleteLevel(ctx context.Context, institutionID, levelID string) error
	// ReorderLevels sets positions in the given order; ids must be exactly the institution's levels
	ReorderLevels(ctx context.Context, institutionID string, ids []string) error

	CreateGrade(ctx context.Context, institutionID, levelID string, in domain.GradeInput) (domain.Grade, error)
	UpdateGrade(ctx context.Context, institutionID, gradeID string, in domain.GradeInput) (domain.Grade, error)
	// DeleteGrade fails with domain.ErrGradeInUse while it has sections
	DeleteGrade(ctx context.Context, institutionID, gradeID string) error
	ReorderGrades(ctx context.Context, institutionID, levelID string, ids []string) error

	// CreateStructure stores a whole template (levels with their grades) in one transaction
	CreateStructure(ctx context.Context, institutionID string, levels []domain.Level) error

	// Periods lists every period with its terms and section totals, newest first
	Periods(ctx context.Context, institutionID string) ([]domain.Period, error)
	Period(ctx context.Context, institutionID, periodID string) (domain.Period, error)
	// CreatePeriod stores the period and its terms in one transaction
	CreatePeriod(ctx context.Context, institutionID string, p domain.Period) (domain.Period, error)
	// UpdatePeriod rewrites the fields; terms, when non-nil, replace the current ones
	UpdatePeriod(ctx context.Context, institutionID string, p domain.Period, terms []domain.Term) (domain.Period, error)
	SetPeriodStatus(ctx context.Context, institutionID, periodID, status string) error
	DeletePeriod(ctx context.Context, institutionID, periodID string) error
	UpdateTerm(ctx context.Context, institutionID, termID string, in domain.TermInput) (domain.Term, error)

	Sections(ctx context.Context, institutionID, periodID string) ([]domain.Section, error)
	Section(ctx context.Context, institutionID, sectionID string) (domain.Section, error)
	// CreateSections stores them all or none
	CreateSections(ctx context.Context, institutionID, periodID string, sections []domain.Section) ([]domain.Section, error)
	UpdateSection(ctx context.Context, institutionID, sectionID string, in domain.SectionInput) (domain.Section, error)
	// OtherGrades counts the active students of a section whose grade is not the section's (multigrade sections)
	OtherGrades(ctx context.Context, sectionID string) (int, error)
	DeleteSection(ctx context.Context, institutionID, sectionID string) error

	// AuthorizedModes is InstitutionRepository.AuthorizedModes: the forms of attention authorized per level on day
	AuthorizedModes(ctx context.Context, institutionID string, day time.Time) (map[string][]string, bool, error)
}
