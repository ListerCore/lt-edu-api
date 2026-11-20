package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// MeetingsRepository keeps what live sessions add: who joined each call, the psychology appointments and the live
// classes of a set of rooms
type MeetingsRepository interface {
	RecordJoin(ctx context.Context, institutionID, source, sourceID, userID, studentID, name string, at time.Time) error
	Joins(ctx context.Context, institutionID, source, sourceID string) ([]domain.LiveJoin, error)

	// LiveItems are the live classes of the rooms starting between from and to. publishedOnly keeps what students
	// see on day: published items of published units already open
	LiveItems(ctx context.Context, institutionID string, roomIDs []string, from, to time.Time, publishedOnly bool, day time.Time) ([]domain.LiveTarget, error)

	SaveAppointment(ctx context.Context, institutionID string, a domain.PsychAppointment) (domain.PsychAppointment, error)
	Appointment(ctx context.Context, institutionID, id string) (domain.PsychAppointment, error)
	SetAppointmentStatus(ctx context.Context, institutionID, id, status string) (domain.PsychAppointment, error)
	// Appointments filters by case, by student or by dates ("" / nil: any)
	Appointments(ctx context.Context, institutionID, caseID, studentID string, from, to *time.Time) ([]domain.PsychAppointment, error)
}
