package domain

import "time"

// Virtual tutoring (phase E6 of docs/ceba/PLAN.md): who tutors each student, the sessions, the messages between
// tutor and student and the referrals to the SAANEE

// Session kinds, modes and statuses
const (
	SessionIndividual = "individual"
	SessionGroup      = "group"

	SessionScheduled = "scheduled"
	SessionHeld      = "held"
	SessionMissed    = "missed"
	SessionCancelled = "cancelled"

	SideStaff   = "staff"
	SideStudent = "student"
)

// TutorStudent is a student with the tutor in force: their own one, or their section's
type TutorStudent struct {
	StudentID     string
	Code          string
	Name          string
	Placement     string
	SectionID     string
	AttentionMode string
	StaffID       string
	StaffName     string
	// The tutor is the student's own (not the section's)
	Individual bool
	// Last action in the platform (nil: never)
	LastAt *time.Time
}

// TutorLoad is how many students a tutor has: through the sections they tutor and assigned one by one
type TutorLoad struct {
	StaffID    string
	Name       string
	Sections   []string
	BySection  int
	Individual int
}

// TutorCandidate is a staff member who can tutor
type TutorCandidate struct {
	StaffID  string
	Name     string
	Position string
}

type SessionAttendee struct {
	StudentID   string
	StudentName string
	Placement   string
	// nil until the session is held
	Attended    *bool
	InterviewID string
}

type TutoringSession struct {
	ID          string
	StaffID     string
	StaffName   string
	Kind        string
	Mode        string
	ScheduledAt time.Time
	DurationMin int
	Link        string
	Provider    string
	Reason      string
	AlertKind   string
	Status      string
	Notes       string
	Agreements  string
	HeldAt      *time.Time
	CreatedName string
	Attendees   []SessionAttendee
}

type SessionFilter struct {
	InstitutionID string
	// Empty: every tutor
	StaffID   string
	StudentID string
	From      *time.Time
	To        *time.Time
}

// Thread is a conversation between a student and a staff member; Unread counts the other side's messages the viewer
// has not read
type Thread struct {
	ID            string
	StudentID     string
	StudentName   string
	Placement     string
	StaffID       string
	StaffName     string
	LastMessageAt *time.Time
	LastBody      string
	LastSide      string
	Unread        int
}

type Message struct {
	ID         string
	ThreadID   string
	Side       string
	AuthorName string
	Body       string
	CreatedAt  time.Time
}

// SAANEE referral statuses
const (
	SaaneeSent      = "sent"
	SaaneeReceived  = "received"
	SaaneeAttending = "attending"
	SaaneeClosed    = "closed"
)

type SaaneeReferral struct {
	ID           string
	StudentID    string
	StudentName  string
	Placement    string
	Reason       string
	Status       string
	SentOn       time.Time
	Reference    string
	Response     string
	ReferredName string
	UpdatedName  string
	UpdatedAt    time.Time
}

// PendingWork is a task or quiz of the student's rooms not handed in, past its deadline or close to it
type PendingWork struct {
	StudentID string
	ItemID    string
	RoomID    string
	Title     string
	Subject   string
	ClosesAt  time.Time
	Overdue   bool
}

var (
	ErrNotTutor          = newError(KindForbidden, "not_tutor", "Solo el tutor del estudiante, dirección o coordinación pueden hacerlo.")
	ErrTutorAssignOffice = newError(KindForbidden, "tutor_assign_office", "Solo dirección y coordinación asignan tutores.")
	ErrNoTutor           = newError(KindConflict, "no_tutor", "Aún no tienes un tutor asignado.")
	ErrSessionClosed     = newError(KindConflict, "session_closed", "La tutoría ya se registró; no se puede cambiar.")
	ErrMessagesForbidden = newError(KindForbidden, "messages_forbidden", "Los mensajes son entre el estudiante y su tutor.")
)
