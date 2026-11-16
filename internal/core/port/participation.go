package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// ParticipationRepository reads the activity events and summaries (attendance.activity_*) and keeps the permanence
// rule and adjustments. Rows of another institution behave as missing
type ParticipationRepository interface {
	InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error)
	// StaffIDByEmail is the active staff record of the account ("" when none)
	StaffIDByEmail(ctx context.Context, institutionID, email string) (string, error)
	// RunningPeriod is the period in course: id, start and end ("" when none)
	RunningPeriod(ctx context.Context, institutionID string) (string, time.Time, time.Time, error)
	Period(ctx context.Context, institutionID, periodID string) (string, time.Time, time.Time, error)

	// Rule is the institution's rule, the default when none was saved
	Rule(ctx context.Context, institutionID string) (domain.PermanenceRule, error)
	SaveRule(ctx context.Context, institutionID string, r domain.PermanenceRule) error

	DistanceSections(ctx context.Context, institutionID, periodID string) ([]domain.DistanceSection, error)
	// Section is a distance section's period, name and tutor (staff id); ErrNotDistanceSection for another form
	Section(ctx context.Context, institutionID, sectionID string) (periodID, label, tutorID string, err error)
	SectionStudents(ctx context.Context, institutionID, sectionID string) ([]domain.PermanenceStudent, error)
	// Activity sums the daily summaries of each student between two days (inclusive), every room together
	Activity(ctx context.Context, studentIDs []string, from, to time.Time) ([]domain.StudentActivity, error)
	Adjustments(ctx context.Context, enrollmentIDs []string) ([]domain.PermanenceAdjustment, error)
	// SaveAdjustment upserts the adjustment of a window, or removes it
	SaveAdjustment(ctx context.Context, institutionID string, a domain.PermanenceAdjustment, remove bool) error
	// EnrollmentSection is the section and student name of an enrollment
	EnrollmentSection(ctx context.Context, institutionID, enrollmentID string) (sectionID, studentName string, err error)

	// RoomParticipation is each student of the room's grade with what they did in it
	RoomParticipation(ctx context.Context, institutionID, roomID string) ([]domain.RoomParticipation, error)
	// DropoutSignals are the active students of the period's distance sections with their last action and overdue work
	DropoutSignals(ctx context.Context, institutionID, periodID string, now time.Time) ([]domain.DropoutSignal, error)
}
