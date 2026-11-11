package port

import (
	"context"

	"api/internal/core/domain"
)

// CurriculumRepository is the study plans part of academic.* in PostgreSQL. Like AcademicRepository, every method
// is scoped to institutionID: rows of another institution behave as missing (domain.ErrNotFound), and unique
// violations come back as domain.ErrDuplicateName
type CurriculumRepository interface {
	// Curricula lists the plans of the institution with their counts and credits, by level then newest first
	Curricula(ctx context.Context, institutionID string) ([]domain.Curriculum, error)
	// Curriculum returns one plan with its modules, subjects (grades and competencies) and transversal competencies
	Curriculum(ctx context.Context, institutionID, id string) (domain.Curriculum, error)
	// CreateCurriculum stores a plan with its whole content in one transaction. Subject.ModuleID holds the code of
	// one of the given modules; Subject.GradeIDs hold grade ids
	CreateCurriculum(ctx context.Context, institutionID string, c domain.Curriculum) (domain.Curriculum, error)
	UpdateCurriculum(ctx context.Context, institutionID, id string, in domain.CurriculumInput) error
	// SetCurriculumStatus changes the status; activating archives the plan that was active for the same level
	SetCurriculumStatus(ctx context.Context, institutionID, id, status string) error
	DeleteCurriculum(ctx context.Context, institutionID, id string) error

	CreateModule(ctx context.Context, institutionID, curriculumID string, in domain.ModuleInput) (domain.Module, error)
	UpdateModule(ctx context.Context, institutionID, id string, in domain.ModuleInput) (domain.Module, error)
	DeleteModule(ctx context.Context, institutionID, id string) error

	// Subject lookups return the plan they belong to, so the service can check its status
	SubjectCurriculum(ctx context.Context, institutionID, subjectID string) (string, error)
	ModuleCurriculum(ctx context.Context, institutionID, moduleID string) (string, error)
	CompetencyCurriculum(ctx context.Context, institutionID, competencyID string) (string, error)

	CreateSubject(ctx context.Context, institutionID, curriculumID string, in domain.SubjectInput) (domain.Subject, error)
	UpdateSubject(ctx context.Context, institutionID, id string, in domain.SubjectInput) (domain.Subject, error)
	DeleteSubject(ctx context.Context, institutionID, id string) error
	ReorderSubjects(ctx context.Context, institutionID, curriculumID string, ids []string) error

	CreateCompetency(ctx context.Context, institutionID, curriculumID, kind string, in domain.CompetencyInput) (domain.Competency, error)
	UpdateCompetency(ctx context.Context, institutionID, id string, in domain.CompetencyInput) (domain.Competency, error)
	DeleteCompetency(ctx context.Context, institutionID, id string) error
	ReorderCompetencies(ctx context.Context, institutionID, curriculumID, subjectID string, ids []string) error
}
