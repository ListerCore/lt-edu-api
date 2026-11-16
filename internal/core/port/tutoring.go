package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// HoldInput is what a held session stores: who came, the notes and agreements, and the interview of each attendee
type HoldInput struct {
	SessionID  string
	Attended   []string
	Notes      string
	Agreements string
	// Interview of each attendee: date (Lima), topic and author
	HeldOn     time.Time
	Topic      string
	AuthorID   string
	AuthorName string
	At         time.Time
}

// TutoringRepository is virtual tutoring in PostgreSQL (wellbeing.tutor_assignments, tutoring_*, threads,
// messages, saanee_referrals). Rows of another institution behave as missing
type TutoringRepository interface {
	// RunningPeriod is the period in course ("" when none)
	RunningPeriod(ctx context.Context, institutionID string) (string, error)
	// TutorStudents are the active students of the period with their tutor in force; staffID filters by tutor
	TutorStudents(ctx context.Context, institutionID, periodID, staffID string) ([]domain.TutorStudent, error)
	TutorLoads(ctx context.Context, institutionID, periodID string) ([]domain.TutorLoad, error)
	// TutorCandidates are the active staff who tutor a section or hold the tutor or teacher role
	TutorCandidates(ctx context.Context, institutionID string) ([]domain.TutorCandidate, error)
	// AssignTutor gives the students a tutor of their own; staffID "" removes it (back to the section's)
	AssignTutor(ctx context.Context, institutionID, periodID string, studentIDs []string, staffID, by string) (int, error)
	// IndividualTutees are the students assigned one by one to the staff member in active periods
	IndividualTutees(ctx context.Context, institutionID, staffID string) ([]string, error)

	Sessions(ctx context.Context, f domain.SessionFilter) ([]domain.TutoringSession, error)
	Session(ctx context.Context, institutionID, id string) (domain.TutoringSession, error)
	// SaveSession inserts or updates a scheduled session and replaces who is called
	SaveSession(ctx context.Context, institutionID string, s domain.TutoringSession, studentIDs []string) (domain.TutoringSession, error)
	// HoldSession marks it held, who came, and writes an interview in the file of each attendee
	HoldSession(ctx context.Context, institutionID string, in HoldInput) (domain.TutoringSession, error)
	SetSessionStatus(ctx context.Context, institutionID, id, status string) error

	// Threads of a staff member or of a student (one of the two ids), newest first; unread from that side
	Threads(ctx context.Context, institutionID, staffID, studentID string) ([]domain.Thread, error)
	Thread(ctx context.Context, institutionID, id string) (domain.Thread, error)
	// EnsureThread is the thread between the student and the staff member, created when missing
	EnsureThread(ctx context.Context, institutionID, studentID, staffID string) (domain.Thread, error)
	Messages(ctx context.Context, threadID string, limit int) ([]domain.Message, error)
	AddMessage(ctx context.Context, threadID string, m domain.Message, authorID string) (domain.Message, error)
	// MarkRead sets when a side last read the thread
	MarkRead(ctx context.Context, threadID, side string, at time.Time) error

	SaaneeReferrals(ctx context.Context, institutionID string, studentIDs []string) ([]domain.SaaneeReferral, error)
	SaaneeReferral(ctx context.Context, institutionID, id string) (domain.SaaneeReferral, error)
	CreateSaanee(ctx context.Context, institutionID string, r domain.SaaneeReferral) (domain.SaaneeReferral, error)
	UpdateSaanee(ctx context.Context, institutionID string, r domain.SaaneeReferral) (domain.SaaneeReferral, error)

	// PendingWork are the tasks and quizzes of the students' rooms not handed in that closed after from and close
	// before to (an extension moves the deadline)
	PendingWork(ctx context.Context, institutionID, periodID string, studentIDs []string, from, to, now time.Time) ([]domain.PendingWork, error)
}
