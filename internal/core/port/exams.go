package port

import (
	"context"

	"api/internal/core/domain"
)

// ExamsRepository is the exams schema in PostgreSQL (academies), scoped to institutionID
type ExamsRepository interface {
	// Courses are the courses of the active plans, grouped by code, with topics, hours per area and bank size
	Courses(ctx context.Context, institutionID string) ([]domain.Course, error)
	Questions(ctx context.Context, f domain.QuestionFilter) ([]domain.Question, int, error)
	Question(ctx context.Context, institutionID, id string) (domain.Question, error)
	SaveQuestion(ctx context.Context, institutionID string, q domain.Question) (domain.Question, error)
	DeleteQuestion(ctx context.Context, institutionID, id string) error
	// PickQuestions draws n random questions of a course, leaving out some ids
	PickQuestions(ctx context.Context, institutionID, courseCode string, n int, exclude []string) ([]domain.Question, error)
	QuestionsByID(ctx context.Context, institutionID string, ids []string) ([]domain.Question, error)

	MockExams(ctx context.Context, institutionID, periodID string) ([]domain.MockExam, error)
	MockExam(ctx context.Context, institutionID, id string) (domain.MockExam, error)
	SaveMockExam(ctx context.Context, institutionID string, m domain.MockExam) (domain.MockExam, error)
	DeleteMockExam(ctx context.Context, institutionID, id string) error
	SetStatus(ctx context.Context, institutionID, id, status string) error
	// SetStructure replaces courses and weights and renumbers the key, keeping what matches
	SetStructure(ctx context.Context, mockID string, courses []domain.MockCourse, weights []domain.MockWeight) error
	Key(ctx context.Context, mockID string) ([]domain.KeyItem, error)
	SaveKey(ctx context.Context, mockID string, items []domain.KeyItem) error

	// PeriodStudents are the students enrolled in the cycle with their classroom and area
	PeriodStudents(ctx context.Context, institutionID, periodID string) ([]domain.ExamStudent, error)
	Sheets(ctx context.Context, mockID string) ([]domain.AnswerSheet, error)
	SaveSheets(ctx context.Context, mockID string, sheets []domain.AnswerSheet) error
	DeleteSheet(ctx context.Context, mockID, studentID string) error
	// StudentSheets are a student's sheets in published (or every, when all) mock exams, oldest first
	StudentSheets(ctx context.Context, institutionID, studentID string, all bool) ([]domain.AnswerSheet, []domain.MockExam, error)

	Admissions(ctx context.Context, institutionID string) ([]domain.Admission, error)
	SaveAdmission(ctx context.Context, institutionID string, a domain.Admission) (domain.Admission, error)
	DeleteAdmission(ctx context.Context, institutionID, id string) error
}
