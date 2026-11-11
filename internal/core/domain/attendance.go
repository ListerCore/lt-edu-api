package domain

import "time"

// Attendance statuses of a day or a class
const (
	MarkPresent   = "present"
	MarkLate      = "late"
	MarkAbsent    = "absent"
	MarkJustified = "justified"
)

// Where a daily mark came from
const (
	SourceGate          = "gate"
	SourceManual        = "manual"
	SourceClosing       = "closing"
	SourceJustification = "justification"
)

// Justification statuses
const (
	JustificationPending  = "pending"
	JustificationApproved = "approved"
	JustificationRejected = "rejected"
)

// CardHolder is whoever an ID card belongs to, as the gate shows them
type CardHolder struct {
	Kind string // student | staff
	ID   string
	Name string
	Code string
	// "3.° B · Secundaria" (students) or the position (staff)
	Detail string
	// Shift of the section (students); staff use the earliest shift of the institution
	Shift string
}

// GateResult is what the gate screen shows after a scan
type GateResult struct {
	Holder    CardHolder
	Status    string
	ArrivedAt time.Time
	// The person had already been marked today: the first arrival is kept
	Repeated bool
}

// GateScan is one read of the recent-scans list
type GateScan struct {
	ID        string
	Holder    CardHolder
	ScannedAt time.Time
	Source    string
	Status    string
}

// DailyMark is the day of one student or staff member
type DailyMark struct {
	PersonID  string
	Day       time.Time
	Status    string
	ArrivedAt *time.Time
	Source    string
	Note      string
}

// RosterRow is one student of a section on one day, with their mark (empty Status: not marked yet)
type RosterRow struct {
	StudentID string
	Code      string
	Name      string
	Status    string
	ArrivedAt *time.Time
	Source    string
	Note      string
	// A justification waiting for review covers the day
	PendingJustification bool
}

// StaffRow is one staff member on one day
type StaffRow struct {
	StaffID   string
	Code      string
	Name      string
	Position  string
	Status    string
	ArrivedAt *time.Time
	Source    string
	Note      string
}

// SectionDay counts a section's marks on one day
type SectionDay struct {
	SectionID    string
	SectionLabel string
	LevelName    string
	Shift        string
	TutorName    string
	Enrolled     int
	Present      int
	Late         int
	Absent       int
	Justified    int
}

// ClassSlot is one class of a teacher's (or a section's) day, with whether its attendance was taken
type ClassSlot struct {
	AssignmentID string
	SectionID    string
	SectionLabel string
	SubjectName  string
	StaffName    string
	BlockID      string
	StartsAt     string
	EndsAt       string
	Room         string
	SessionID    string
	Topic        string
	Absent       int
	Students     int
}

// ClassSession is the attendance of one class
type ClassSession struct {
	ID           string
	AssignmentID string
	SectionID    string
	Day          time.Time
	BlockID      string
	Topic        string
	TakenAt      time.Time
	TakenBy      string
}

// ClassMark is one student in a class
type ClassMark struct {
	StudentID string
	Code      string
	Name      string
	Status    string
	Note      string
	// The day's mark (gate or closing), to suggest the class mark
	DayStatus string
}

// Justification explains absences between two dates
type Justification struct {
	ID            string
	StudentID     string
	StudentName   string
	SectionLabel  string
	FromDay       time.Time
	ToDay         time.Time
	Reason        string
	Description   string
	Status        string
	SubmittedName string
	SubmittedAt   time.Time
	ReviewedName  string
	ReviewedAt    *time.Time
	ReviewNote    string
	// Absences of the student inside the dates (days)
	AbsentDays int
}

type JustificationFilter struct {
	InstitutionID string
	Status        string
	StudentID     string
	// The sections a tutor follows; empty: every section
	SectionIDs []string
	Limit      int
	Offset     int
}

// Tally counts the marks of a student in some days (daily) or classes
type Tally struct {
	Present   int
	Late      int
	Absent    int
	Justified int
}

// Total is every day or class with a mark
func (t Tally) Total() int { return t.Present + t.Late + t.Absent + t.Justified }

// AbsentPercent is the share of unjustified absences, 0 to 100
func (t Tally) AbsentPercent() float64 {
	if t.Total() == 0 {
		return 0
	}
	return float64(t.Absent) * 100 / float64(t.Total())
}

// StudentTally is the attendance of a student in a period
type StudentTally struct {
	StudentID    string
	Code         string
	Name         string
	SectionID    string
	SectionLabel string
	Days         Tally
}

// SubjectTally is the attendance of a student to the classes of one subject (institutes: the 30 % rule)
type SubjectTally struct {
	StudentID    string
	StudentName  string
	SectionLabel string
	AssignmentID string
	SubjectName  string
	Classes      Tally
}

var (
	ErrAttendanceForbidden = newError(KindForbidden, "attendance_forbidden", "Tu rol no puede ver la asistencia.")
	ErrAttendanceReadOnly  = newError(KindForbidden, "attendance_read_only", "Tu rol solo puede consultar la asistencia.")
	ErrCardUnknown         = newError(KindNotFound, "card_unknown", "No reconocemos ese carnet. Verifica que sea de esta institución.")
	ErrCardInactive        = newError(KindConflict, "card_inactive", "La persona del carnet no está activa en la institución.")
	ErrNotEnrolledToday    = newError(KindConflict, "not_enrolled_today", "El estudiante no tiene matrícula vigente hoy.")
	ErrNotSchoolDay        = newError(KindInvalid, "not_school_day", "Ese día no hay clases según la configuración de la institución.")
	ErrFutureDay           = newError(KindInvalid, "future_day", "No se puede registrar asistencia de días que aún no llegan.")
	ErrNotYourClass        = newError(KindForbidden, "not_your_class", "Solo el docente del curso o coordinación puede tomar esta asistencia.")
	ErrNoSuchClass         = newError(KindInvalid, "no_such_class", "Ese curso no tiene clase en esa hora del horario.")
	ErrJustificationClosed = newError(KindConflict, "justification_closed", "La justificación ya fue revisada.")
	ErrNotYourStudent      = newError(KindForbidden, "not_your_student", "Solo puedes justificar a tus hijos o a ti mismo.")
)
