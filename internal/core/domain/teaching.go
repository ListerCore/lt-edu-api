package domain

import "time"

// Staff is someone who works at the institution; teachers can be given subjects and sections
type Staff struct {
	ID             string
	Code           string
	DocumentType   string
	DocumentNumber string
	GivenNames     string
	Surnames       string
	Email          string
	Phone          string
	Position       string
	IsTeacher      bool
	Specialty      string
	MaxWeeklyHours *int
	HiredOn        *time.Time
	Status         string
	Notes          string
	// Panel roles the email holds in the institution, and the ones invited and not yet accepted
	Roles   []string
	Invited []string
	// Weekly hours assigned in running periods, and the sections the person tutors there
	AssignedHours int
	TutorOf       []string
}

type StaffInput struct {
	Code           string
	DocumentType   string
	DocumentNumber string
	GivenNames     string
	Surnames       string
	Email          string
	Phone          string
	Position       string
	IsTeacher      bool
	Specialty      string
	MaxWeeklyHours *int
	HiredOn        *time.Time
	Status         string
	Notes          string
}

type StaffFilter struct {
	InstitutionID string
	Query         string
	Status        string
	TeachersOnly  bool
	Limit         int
	Offset        int
}

// TimeBlock is one slot of the school day of a shift: a class hour or a break. Times read "HH:MM"
type TimeBlock struct {
	ID       string
	Shift    string
	Position int
	StartsAt string
	EndsAt   string
	Kind     string
}

// Assignment is the teaching load of one subject in one section
type Assignment struct {
	ID          string
	PeriodID    string
	SectionID   string
	SubjectID   string
	SubjectName string
	SubjectKind string
	SubjectCode string
	StaffID     string
	StaffName   string
	WeeklyHours int
	// Hours already placed in the timetable
	Scheduled int
	// "3.° B · Secundaria" and the period, for lists outside the planner
	SectionLabel string
	PeriodName   string
}

// ScheduleEntry is one class in the timetable, with what the panel shows about it
type ScheduleEntry struct {
	ID           string
	PeriodID     string
	SectionID    string
	SectionLabel string
	Shift        string
	AssignmentID string
	SubjectName  string
	StaffID      string
	StaffName    string
	Weekday      int
	BlockID      string
	StartsAt     string
	EndsAt       string
	Room         string
}

// Slot is a class placed by the automatic timetable (before it is stored)
type Slot struct {
	SectionID    string
	AssignmentID string
	Weekday      int
	BlockID      string
}

var (
	ErrStaffForbidden    = newError(KindForbidden, "staff_forbidden", "Tu rol no puede ver al personal.")
	ErrStaffReadOnly     = newError(KindForbidden, "staff_read_only", "Solo dirección y coordinación pueden hacer cambios aquí.")
	ErrStaffInUse        = newError(KindConflict, "staff_in_use", "Tiene carga docente o tutoría. Quítasela o márcalo como inactivo.")
	ErrTeacherBusy       = newError(KindConflict, "teacher_busy", "El docente ya tiene clase a esa hora en otra sección.")
	ErrRoomBusy          = newError(KindConflict, "room_busy", "El ambiente ya está ocupado a esa hora.")
	ErrHoursComplete     = newError(KindConflict, "hours_complete", "Ese curso ya tiene todas sus horas en el horario.")
	ErrNotATeacher       = newError(KindInvalid, "not_a_teacher", "Esa persona no está registrada como docente activo.")
	ErrScheduleForbidden = newError(KindForbidden, "schedule_forbidden", "Tu rol no puede ver la carga docente.")
)

// PersonalSection is a section someone follows: their own (student) or a child's (guardian)
type PersonalSection struct {
	StudentName  string
	SectionID    string
	SectionLabel string
	LevelName    string
	PeriodID     string
	PeriodName   string
	Shift        string
}
