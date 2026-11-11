package domain

import "time"

// Student statuses
const (
	StudentActive    = "active"
	StudentInactive  = "inactive"
	StudentGraduated = "graduated"
	StudentWithdrawn = "withdrawn"
)

// Enrollment kinds and statuses
const (
	EnrollNew        = "new"
	EnrollContinuing = "continuing"
	EnrollTransferIn = "transfer_in"
	EnrollReEntry    = "re_entry"

	EnrollmentActive      = "active"
	EnrollmentWithdrawn   = "withdrawn"
	EnrollmentTransferred = "transferred"
	EnrollmentCompleted   = "completed"
)

// Account access of a student or guardian: none, invited (pending sign-up) or account (signed up)
const (
	AccessNone    = "none"
	AccessInvited = "invited"
	AccessAccount = "account"
)

// EnrollmentSummary is where a student is in a period, for lists
type EnrollmentSummary struct {
	ID          string
	PeriodID    string
	PeriodName  string
	LevelID     string
	LevelName   string
	GradeID     string
	GradeName   string
	GradeShort  string
	SectionID   string
	SectionName string
	Shift       string
	Status      string
}

type Student struct {
	ID              string
	InstitutionID   string
	Code            string
	DocumentType    string
	DocumentNumber  string
	GivenNames      string
	PaternalSurname string
	MaternalSurname string
	BirthDate       *time.Time
	Sex             string
	Email           string
	Phone           string
	Address         string
	SpecialNeeds    bool
	Notes           string
	Status          string
	Access          string
	CreatedAt       time.Time
	// Lists: the enrollment in the period asked for (or the latest), the main guardian and how many are linked
	Enrollment     *EnrollmentSummary
	PrimaryName    string
	PrimaryPhone   string
	GuardianCount  int
}

// FullName reads "Paterno Materno, Nombres"
func (s Student) FullName() string {
	surnames := s.PaternalSurname
	if s.MaternalSurname != "" {
		surnames += " " + s.MaternalSurname
	}
	return surnames + ", " + s.GivenNames
}

type Guardian struct {
	ID             string
	DocumentType   string
	DocumentNumber string
	GivenNames     string
	Surnames       string
	Email          string
	Phone          string
	Occupation     string
	Access         string
	// When read through a student: the link
	Relationship string
	IsPrimary    bool
	CanPickUp    bool
	LivesWith    bool
	// Other students this guardian answers for
	StudentCount int
}

type StudentInput struct {
	Code            string
	DocumentType    string
	DocumentNumber  string
	GivenNames      string
	PaternalSurname string
	MaternalSurname string
	BirthDate       *time.Time
	Sex             string
	Email           string
	Phone           string
	Address         string
	SpecialNeeds    bool
	Notes           string
	Status          string
}

type GuardianInput struct {
	DocumentType   string
	DocumentNumber string
	GivenNames     string
	Surnames       string
	Email          string
	Phone          string
	Occupation     string
}

type GuardianLink struct {
	Relationship string
	IsPrimary    bool
	CanPickUp    bool
	LivesWith    bool
}

type StudentFilter struct {
	InstitutionID string
	Query         string
	Status        string
	// Enrollment filters apply in PeriodID (empty: no period filter; the summary shows the latest enrollment)
	PeriodID  string
	LevelID   string
	GradeID   string
	SectionID string
	// Only students without an enrollment in PeriodID
	NotEnrolled bool
	Limit       int
	Offset      int
}

// Enrollment is a student in a period
type Enrollment struct {
	ID            string
	StudentID     string
	StudentName   string
	StudentCode   string
	StudentDoc    string
	PeriodID      string
	PeriodName    string
	LevelID       string
	LevelName     string
	GradeID       string
	GradeName     string
	GradeShort    string
	SectionID     string
	SectionName   string
	Shift         string
	AttentionMode string
	Kind          string
	Status        string
	EnrolledOn    time.Time
	EndedOn       *time.Time
	Origin        string
	Destination   string
	Notes         string
	// Institutes: the didactic units taken in the period
	UnitIDs []string
	Credits int
}

type EnrollmentInput struct {
	StudentID  string
	PeriodID   string
	LevelID    string
	GradeID    string
	SectionID  string
	Kind       string
	EnrolledOn time.Time
	Origin     string
	Notes      string
	UnitIDs    []string
}

type EnrollmentFilter struct {
	InstitutionID string
	PeriodID      string
	LevelID       string
	GradeID       string
	SectionID     string
	Status        string
	Query         string
	Limit         int
	Offset        int
}

// SectionOccupancy is how full a section is in its period
type SectionOccupancy struct {
	SectionID string
	Active    int
	Capacity  int
}

type Placement struct {
	ID          string
	StudentID   string
	StudentName string
	Kind        string
	LevelID     string
	LevelName   string
	GradeID     string
	GradeName   string
	DecidedOn   time.Time
	Reference   string
	Notes       string
}

type PlacementInput struct {
	StudentID string
	Kind      string
	LevelID   string
	GradeID   string
	DecidedOn time.Time
	Reference string
	Notes     string
}

var (
	ErrPeopleForbidden    = newError(KindForbidden, "people_forbidden", "Tu rol no puede ver la información de los estudiantes.")
	ErrPeopleReadOnly     = newError(KindForbidden, "people_read_only", "Solo dirección, coordinación y secretaría pueden hacer cambios aquí.")
	ErrStudentEnrolled    = newError(KindConflict, "student_enrolled", "El estudiante tiene matrículas. Cambia su estado en lugar de eliminarlo.")
	ErrAlreadyEnrolled    = newError(KindConflict, "already_enrolled", "El estudiante ya está matriculado en ese periodo.")
	ErrSectionFull        = newError(KindConflict, "section_full", "La sección no tiene vacantes. Elige otra o aumenta sus vacantes.")
	ErrStudentNotActive   = newError(KindConflict, "student_not_active", "Solo se matricula a estudiantes activos.")
	ErrNoEmailForAccess   = newError(KindInvalid, "no_email", "Registra un correo antes de darle acceso al panel.")
	ErrAlreadyHasAccess   = newError(KindConflict, "already_has_access", "Ya tiene acceso al panel con ese correo.")
	ErrGuardianLinkExists = newError(KindConflict, "guardian_linked", "Ese apoderado ya está vinculado a este estudiante.")
)
