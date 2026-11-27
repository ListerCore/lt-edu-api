package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// AssessmentRepository is the assessment schema in PostgreSQL, scoped to institutionID. termID "" means the whole
// period (IEST semesters and CEPRE cycles have no terms)
type AssessmentRepository interface {
	Section(ctx context.Context, institutionID, sectionID string) (domain.SectionMeta, error)
	// Students are the active students of the section; with subjectID, only those who take it (IEST units)
	Students(ctx context.Context, institutionID, sectionID, subjectID string) ([]domain.StudentRef, error)
	// Competencies of an area; Transversal ones of the plan the section studies
	Competencies(ctx context.Context, institutionID, subjectID string) ([]domain.CompetencyRef, error)
	Transversal(ctx context.Context, institutionID, sectionID string) ([]domain.CompetencyRef, error)

	// CompetencyMarks of a section; empty termID with all=true returns every term (report cards)
	CompetencyMarks(ctx context.Context, institutionID, sectionID, termID string, all bool) ([]domain.CompetencyMark, error)
	// SaveCompetencyMarks upserts; an empty level and conclusion removes the mark
	SaveCompetencyMarks(ctx context.Context, institutionID, sectionID, termID string, marks []domain.CompetencyMark, userID string) error

	Evaluations(ctx context.Context, institutionID, assignmentID, termID string) ([]domain.Evaluation, error)
	// SectionEvaluations are every evaluation of the section's assignments in the period (report cards)
	SectionEvaluations(ctx context.Context, institutionID, sectionID string) ([]domain.Evaluation, error)
	Evaluation(ctx context.Context, institutionID, id string) (domain.Evaluation, error)
	SaveEvaluation(ctx context.Context, institutionID string, e domain.Evaluation) (domain.Evaluation, error)
	DeleteEvaluation(ctx context.Context, institutionID, id string) error
	Scores(ctx context.Context, evaluationIDs []string) ([]domain.Score, error)
	// SaveScores upserts; nil removes the score
	SaveScores(ctx context.Context, evaluationID string, scores map[string]*int) error

	Conduct(ctx context.Context, institutionID, sectionID, termID string, all bool) ([]domain.ConductMark, error)
	// SaveConduct upserts; nil removes it
	SaveConduct(ctx context.Context, institutionID, sectionID, termID string, scores map[string]*int, notes map[string]string) error
	Comments(ctx context.Context, sectionID, termID string) ([]domain.TutorComment, error)
	SaveComments(ctx context.Context, sectionID, termID string, comments map[string]string) error

	Submissions(ctx context.Context, institutionID, periodID, termID string) ([]domain.Submission, error)
	SetSubmission(ctx context.Context, institutionID, sectionID, assignmentID, termID string, sent bool, userID, name string) error
	// Progress of the assignments of a period in a term (literal or numeric, by the scale); staffID limits it to
	// one teacher's courses ("" = every course)
	Progress(ctx context.Context, institutionID, periodID, termID string, numeric bool, staffID string) ([]domain.SheetProgress, error)

	// StudentSection is the section of the student's enrollment in a period
	StudentSection(ctx context.Context, institutionID, studentID, periodID string) (string, error)
	// StudentPeriods are the periods (newest first) where the student has an enrollment with a section
	StudentPeriods(ctx context.Context, institutionID, studentID string) ([]string, error)
	// AttendanceBetween counts the daily marks of a student between two dates
	AttendanceBetween(ctx context.Context, institutionID, studentID string, from, to time.Time) (domain.Tally, error)
	// SubjectsOfSection lists the assignments of a section with the subject kind and position (report card rows)
	SubjectsOfSection(ctx context.Context, institutionID, sectionID string) ([]domain.Assignment, error)
}

// AssessmentSeeder writes the demo grades in bulk (cmd/seed only)
type AssessmentSeeder interface {
	HasAssessment(ctx context.Context, institutionID string) (bool, error)
	BulkCompetencyMarks(ctx context.Context, institutionID string, rows []SeedMark) (int, error)
	BulkConduct(ctx context.Context, institutionID string, rows []SeedConduct) error
	BulkComments(ctx context.Context, rows []SeedComment) error
	BulkScores(ctx context.Context, scores []domain.Score) error
}

type SeedMark struct {
	SectionID string
	TermID    string
	domain.CompetencyMark
}

type SeedConduct struct {
	SectionID string
	TermID    string
	StudentID string
	Score     int
}

type SeedComment struct {
	SectionID string
	TermID    string
	StudentID string
	Comment   string
}
