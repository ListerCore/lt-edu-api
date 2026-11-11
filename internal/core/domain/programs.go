package domain

import "time"

// ProgramInfo is an IEST study program (a level) with its plan in force and coordinator
type ProgramInfo struct {
	LevelID         string
	Code            string
	Name            string
	CurriculumID    string
	CoordinatorID   string
	CoordinatorName string
	Students        int
}

type ModuleRef struct {
	ID       string
	Code     string
	Name     string
	Position int
}

// UnitRef is a didactic unit or EFSRT of the plan with its semester
type UnitRef struct {
	SubjectID       string
	ModuleID        string
	Kind            string
	Code            string
	Name            string
	TheoryCredits   int
	PracticeCredits int
	Semester        string
	SemesterPos     int
}

func (u UnitRef) Credits() int { return u.TheoryCredits + u.PracticeCredits }

// UnitStanding is where a student is in a unit: approved, failed, or taking it now
type UnitStanding struct {
	SubjectID  string
	Status     string // approved | failed | ongoing
	Grade      *int
	PeriodName string
	Source     string // closing | history | validation | enrolled
}

// ProgramStudent is a student of a program with the semester of their latest enrollment
type ProgramStudent struct {
	ID          string
	Code        string
	Name        string
	Document    string
	Status      string
	LevelID     string
	Semester    string
	SemesterPos int
}

type Internship struct {
	ID                string
	StudentID         string
	StudentName       string
	StudentCode       string
	SubjectID         string
	SubjectName       string
	ModuleName        string
	LevelID           string
	ProgramName       string
	Company           string
	CompanyRUC        string
	Area              string
	Supervisor        string
	SupervisorContact string
	StartsOn          time.Time
	EndsOn            *time.Time
	RequiredHours     int
	LoggedHours       float64
	Status            string
	Score             *int
	Evaluation        string
	EvaluatedName     string
	EvaluatedAt       *time.Time
}

type InternshipFilter struct {
	InstitutionID string
	LevelIDs      []string
	StudentID     string
	Status        string
}

type InternshipLog struct {
	ID       string
	Day      time.Time
	Hours    float64
	Activity string
}

type Certificate struct {
	ID          string
	StudentID   string
	StudentName string
	StudentCode string
	LevelID     string
	ProgramName string
	Kind        string
	ModuleID    string
	ModuleName  string
	Number      int
	Code        string
	Status      string
	Content     []byte
	IssuedOn    time.Time
	IssuedName  string
	AnnulReason string
}

type Requirement struct {
	Code        string
	Done        bool
	Note        string
	CheckedName string
	CheckedAt   *time.Time
}

var (
	ErrProgramsForbidden  = newError(KindForbidden, "programs_forbidden", "Tu rol no puede ver los programas de estudios.")
	ErrProgramsReadOnly   = newError(KindForbidden, "programs_read_only", "Solo dirección, coordinación o secretaría académica hacen este trámite.")
	ErrNotAnInstitute     = newError(KindInvalid, "not_an_institute", "Los módulos, las experiencias formativas y la titulación son de los institutos.")
	ErrNotYourProgram     = newError(KindForbidden, "not_your_program", "No coordinas ese programa de estudios.")
	ErrNotEFSRT           = newError(KindInvalid, "not_efsrt", "Elige una experiencia formativa del plan del estudiante.")
	ErrInternshipClosed   = newError(KindConflict, "internship_closed", "La experiencia formativa ya fue evaluada.")
	ErrHoursMissing       = newError(KindConflict, "hours_missing", "Aún no completa las horas exigidas: no se puede aprobar.")
	ErrModuleIncomplete   = newError(KindConflict, "module_incomplete", "El módulo aún no está completo: faltan unidades o la experiencia formativa.")
	ErrDegreeRequirements = newError(KindConflict, "degree_requirements", "Aún no cumple todos los requisitos de titulación.")
	ErrAlreadyIssued      = newError(KindConflict, "already_issued", "Ya tiene ese certificado emitido.")
)
