package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// WellStudentFilter narrows the students of the wellbeing screens (current enrollment). Nil slices mean "any"
type WellStudentFilter struct {
	InstitutionID string
	SectionIDs    []string
	StudentIDs    []string
	Query         string
	SpecialNeeds  bool
}

// TherapyFilter narrows the therapy log
type TherapyFilter struct {
	InstitutionID string
	StudentIDs    []string
	TherapistID   string
	From, To      *time.Time
}

// LowConduct is a student whose latest conduct grade fails (below 11)
type LowConduct struct {
	StudentID   string
	StudentName string
	Placement   string
	SectionID   string
	Score       int
	TermName    string
}

// MeasureRow is a measure with its student, for the register of measures and the alerts
type MeasureRow struct {
	domain.Measure
	StudentName string
	Placement   string
}

// WellbeingRepository is the wellbeing schema, scoped to institutionID
type WellbeingRepository interface {
	Students(ctx context.Context, f WellStudentFilter) ([]domain.WellStudent, error)
	// TeacherSections are the sections a staff member teaches in active periods
	TeacherSections(ctx context.Context, institutionID, staffID string) ([]string, error)
	// TermOf is the term of the active period that covers day (empty when none)
	TermOf(ctx context.Context, institutionID string, day time.Time) (id string, from, to time.Time, err error)
	// CurrentPeriod is the active period covering day, or the latest active one
	CurrentPeriod(ctx context.Context, institutionID string, day time.Time) (id, name string, err error)
	Specialists(ctx context.Context, institutionID string) ([]domain.Staff, error)

	Interviews(ctx context.Context, institutionID string, studentIDs []string, from *time.Time) ([]domain.Interview, error)
	Interview(ctx context.Context, institutionID, id string) (domain.Interview, error)
	SaveInterview(ctx context.Context, institutionID string, x domain.Interview) (domain.Interview, error)
	DeleteInterview(ctx context.Context, institutionID, id string) error

	Commitments(ctx context.Context, institutionID string, studentIDs []string, pendingOnly bool) ([]domain.Commitment, error)
	Commitment(ctx context.Context, institutionID, id string) (domain.Commitment, error)
	SaveCommitment(ctx context.Context, institutionID string, c domain.Commitment) (domain.Commitment, error)
	CheckCommitment(ctx context.Context, institutionID, id, status string, on *time.Time) error
	DeleteCommitment(ctx context.Context, institutionID, id string) error

	Cases(ctx context.Context, institutionID string, studentIDs []string, status, referredBy string) ([]domain.PsychCase, error)
	Case(ctx context.Context, institutionID, id string) (domain.PsychCase, error)
	OpenCase(ctx context.Context, institutionID string, c domain.PsychCase) (domain.PsychCase, error)
	UpdateCase(ctx context.Context, institutionID string, c domain.PsychCase) error
	Sessions(ctx context.Context, caseID string) ([]domain.PsychSession, error)
	AddSession(ctx context.Context, s domain.PsychSession) (domain.PsychSession, error)
	DeleteSession(ctx context.Context, caseID, id string) error

	Incidents(ctx context.Context, f domain.IncidentFilter) ([]domain.Incident, error)
	Incident(ctx context.Context, institutionID, id string) (domain.Incident, error)
	SaveIncident(ctx context.Context, institutionID string, x domain.Incident) (domain.Incident, error)
	DeleteIncident(ctx context.Context, institutionID, id string) error
	// Measures of a student taken from a day on (the school year)
	Measures(ctx context.Context, institutionID, studentID string, from time.Time) ([]domain.Measure, error)
	Measure(ctx context.Context, institutionID, id string) (domain.Measure, error)
	// MeasureRows are the suspensions, conditional enrollments and expulsions from a day on (nil students: all)
	MeasureRows(ctx context.Context, institutionID string, studentIDs []string, from time.Time) ([]MeasureRow, error)
	AddMeasure(ctx context.Context, institutionID string, m domain.Measure) (domain.Measure, error)
	NotifyMeasure(ctx context.Context, institutionID, id string, on time.Time) error
	RequestReconsideration(ctx context.Context, institutionID, id string, on time.Time, reason, by string) error
	ResolveReconsideration(ctx context.Context, institutionID, id string, on time.Time, outcome, resolution string) error
	DeleteMeasure(ctx context.Context, institutionID, id string) error
	// Minor incidents of a student between two days
	CountIncidents(ctx context.Context, institutionID, studentID, severity string, from, to time.Time) (int, error)
	Conduct(ctx context.Context, institutionID, studentID, periodID string) ([]domain.TermConduct, error)
	LowConduct(ctx context.Context, institutionID, periodID string, sectionIDs []string) ([]LowConduct, error)

	Plans(ctx context.Context, institutionID, periodID, specialistID string, studentIDs []string) ([]domain.IndividualPlan, error)
	Plan(ctx context.Context, institutionID, id string) (domain.IndividualPlan, error)
	SavePlan(ctx context.Context, institutionID string, p domain.IndividualPlan) (domain.IndividualPlan, error)
	SetPlanStatus(ctx context.Context, institutionID, id, status, name string, on *time.Time) error
	DeletePlan(ctx context.Context, institutionID, id string) error

	Therapies(ctx context.Context, f TherapyFilter) ([]domain.TherapySession, error)
	Therapy(ctx context.Context, institutionID, id string) (domain.TherapySession, error)
	SaveTherapy(ctx context.Context, institutionID string, t domain.TherapySession) (domain.TherapySession, error)
	DeleteTherapy(ctx context.Context, institutionID, id string) error
}
