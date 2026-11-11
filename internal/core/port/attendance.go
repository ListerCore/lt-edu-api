package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// AttendanceRepository is the attendance schema in PostgreSQL, scoped to institutionID. Days are calendar dates
// (midnight UTC); arrivals are instants
type AttendanceRepository interface {
	// HolderByToken finds the owner of an ID card token ("student" or "staff"); on is the day whose section is shown
	HolderByToken(ctx context.Context, institutionID, kind, token string, on time.Time) (domain.CardHolder, string, error)
	// HolderByCode finds a student or staff member by code or document number (cards typed by hand)
	HolderByCode(ctx context.Context, institutionID, code string, on time.Time) (domain.CardHolder, string, error)
	// SectionOn is the section of the student's active enrollment in an active period that covers the day
	SectionOn(ctx context.Context, institutionID, studentID string, on time.Time) (sectionID, shift string, err error)
	CardTokens(ctx context.Context, institutionID, kind string, ids []string) (map[string]string, error)
	RenewCardToken(ctx context.Context, institutionID, kind, id string) (string, error)

	RecordScan(ctx context.Context, institutionID string, holder domain.CardHolder, at time.Time, source, userID string) error
	RecentScans(ctx context.Context, institutionID string, since time.Time, limit int) ([]domain.GateScan, error)

	// DailyMark returns the day of a student or staff member (kind), domain.ErrNotFound when not marked
	DailyMark(ctx context.Context, institutionID, kind, personID string, day time.Time) (domain.DailyMark, error)
	// SaveDailyMark creates or replaces the mark of the day
	SaveDailyMark(ctx context.Context, institutionID, kind string, m domain.DailyMark, userID string) error
	SectionDays(ctx context.Context, institutionID string, day time.Time) ([]domain.SectionDay, error)
	Roster(ctx context.Context, institutionID, sectionID string, day time.Time) ([]domain.RosterRow, error)
	StaffDay(ctx context.Context, institutionID string, day time.Time) ([]domain.StaffRow, error)
	// CloseDay marks absent (justified when an approved justification covers the day) the enrolled students without a
	// mark; empty sectionIDs means every section of the day. Returns how many were marked
	CloseDay(ctx context.Context, institutionID string, day time.Time, sectionIDs []string, userID string) (int, error)
	CloseStaffDay(ctx context.Context, institutionID string, day time.Time) (int, error)
	// TutorSections are the sections of active periods the staff member tutors
	TutorSections(ctx context.Context, institutionID, staffID string) ([]string, error)

	// Sessions returns the classes taken on a day among some assignments, by "assignmentID|blockID"
	Sessions(ctx context.Context, institutionID string, day time.Time, assignmentIDs []string) (map[string]domain.ClassSlot, error)
	// ClassRoster lists the students of the section who take the subject, with their marks in the session (if any)
	ClassRoster(ctx context.Context, institutionID, sectionID, subjectID, sessionID string, day time.Time) ([]domain.ClassMark, error)
	SaveSession(ctx context.Context, institutionID string, s domain.ClassSession, marks []domain.ClassMark) (string, error)

	CreateJustification(ctx context.Context, institutionID string, j domain.Justification, userID string) (domain.Justification, error)
	Justifications(ctx context.Context, f domain.JustificationFilter) ([]domain.Justification, int, error)
	Justification(ctx context.Context, institutionID, id string) (domain.Justification, error)
	// ReviewJustification closes it; approving turns the absences (daily and per class) in its dates into justified
	ReviewJustification(ctx context.Context, institutionID, id, status, note, userID, userName string) (domain.Justification, error)

	// StudentTallies counts the days of the students enrolled in a period (one section, or all)
	StudentTallies(ctx context.Context, institutionID, periodID, sectionID string) ([]domain.StudentTally, error)
	// SubjectTallies counts the classes per subject of the students of a period (one section or one student)
	SubjectTallies(ctx context.Context, institutionID, periodID, sectionID, studentID string) ([]domain.SubjectTally, error)
	DailyMarks(ctx context.Context, institutionID, studentID string, from, to time.Time) ([]domain.DailyMark, error)
	// StudentPeriod is the active period and section of the student today (or the latest one), with the student's name
	StudentPeriod(ctx context.Context, institutionID, studentID string) (periodID, sectionID, sectionLabel, name string, err error)
	// StudentsForEmail are the students with that email, or whose guardian has it
	StudentsForEmail(ctx context.Context, institutionID, email string) ([]domain.CardHolder, error)
}

// SeedStudent is an enrolled student as the demo attendance seed needs them
type SeedStudent struct {
	StudentID    string
	SectionID    string
	Shift        string
	GuardianName string
}

// AttendanceSeeder is what the demo seed writes in bulk (cmd/seed only)
type AttendanceSeeder interface {
	SeedRoster(ctx context.Context, institutionID, periodID string) ([]SeedStudent, error)
	// BulkDailyMarks stores many student marks at once (COPY); the days must be free
	BulkDailyMarks(ctx context.Context, institutionID string, marks []domain.DailyMark) (int, error)
	BulkStaffMarks(ctx context.Context, institutionID string, marks []domain.DailyMark) (int, error)
	HasAttendance(ctx context.Context, institutionID string) (bool, error)
}
