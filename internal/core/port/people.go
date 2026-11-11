package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// PeopleRepository is people.* and enrollment.* in PostgreSQL. Like the academic repositories, every method is
// scoped to institutionID (rows of another institution behave as missing) and unique violations come back as
// domain.ErrDuplicateName unless a method says otherwise
type PeopleRepository interface {
	// Students returns one page and the total; each row carries its enrollment in filter.PeriodID (or its latest)
	Students(ctx context.Context, f domain.StudentFilter) ([]domain.Student, int, error)
	Student(ctx context.Context, institutionID, id string) (domain.Student, error)
	// CreateStudent fills an empty code with the next "<year>-NNNN" of the institution
	CreateStudent(ctx context.Context, institutionID string, in domain.StudentInput) (domain.Student, error)
	UpdateStudent(ctx context.Context, institutionID, id string, in domain.StudentInput) (domain.Student, error)
	// DeleteStudent fails with domain.ErrStudentEnrolled while the student has enrollments
	DeleteStudent(ctx context.Context, institutionID, id string) error

	StudentGuardians(ctx context.Context, institutionID, studentID string) ([]domain.Guardian, error)
	Guardian(ctx context.Context, institutionID, id string) (domain.Guardian, error)
	GuardianByDocument(ctx context.Context, institutionID, documentType, documentNumber string) (domain.Guardian, error)
	CreateGuardian(ctx context.Context, institutionID string, in domain.GuardianInput) (domain.Guardian, error)
	UpdateGuardian(ctx context.Context, institutionID, id string, in domain.GuardianInput) (domain.Guardian, error)
	// LinkGuardian links (or, when already linked, updates the link); a primary link makes the others secondary
	LinkGuardian(ctx context.Context, institutionID, studentID, guardianID string, link domain.GuardianLink) error
	IsLinked(ctx context.Context, studentID, guardianID string) (bool, error)
	// UnlinkGuardian removes the link, and the guardian too when it answers for no other student
	UnlinkGuardian(ctx context.Context, institutionID, studentID, guardianID string) error

	// GrantAccess gives the panel to an email: the role at once when the account exists, an invitation otherwise.
	// Returns the resulting access (domain.AccessAccount or domain.AccessInvited)
	GrantAccess(ctx context.Context, institutionID, email, roleCode, invitedBy string) (string, error)

	Enrollments(ctx context.Context, f domain.EnrollmentFilter) ([]domain.Enrollment, int, error)
	Enrollment(ctx context.Context, institutionID, id string) (domain.Enrollment, error)
	StudentEnrollments(ctx context.Context, institutionID, studentID string) ([]domain.Enrollment, error)
	// EnrollmentOf returns the enrollment of a student in a period, or domain.ErrNotFound
	EnrollmentOf(ctx context.Context, institutionID, studentID, periodID string) (domain.Enrollment, error)
	// CreateEnrollment stores it with its units; a second one for the same student and period is domain.ErrAlreadyEnrolled
	CreateEnrollment(ctx context.Context, institutionID string, in domain.EnrollmentInput) (domain.Enrollment, error)
	// UpdateEnrollment moves it (grade, section) and rewrites notes; units, when non-nil, replace the current ones
	UpdateEnrollment(ctx context.Context, institutionID, id, gradeID, sectionID, notes string, unitIDs []string) (domain.Enrollment, error)
	SetEnrollmentStatus(ctx context.Context, institutionID, id, status string, endedOn *time.Time, destination string) (domain.Enrollment, error)
	DeleteEnrollment(ctx context.Context, institutionID, id string) error
	// Occupancy counts the active enrollments of every section of a period
	Occupancy(ctx context.Context, institutionID, periodID string) ([]domain.SectionOccupancy, error)

	Placements(ctx context.Context, institutionID, studentID string) ([]domain.Placement, error)
	CreatePlacement(ctx context.Context, institutionID string, in domain.PlacementInput) (domain.Placement, error)
	DeletePlacement(ctx context.Context, institutionID, id string) error
}
