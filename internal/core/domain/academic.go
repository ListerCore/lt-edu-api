package domain

import "time"

// Institution kinds (iam.institutions.kind)
const (
	KindEBR       = "ebr"
	KindEBA       = "eba"
	KindEBE       = "ebe"
	KindCEPRE     = "cepre"
	KindInstitute = "institute"
)

// Term schemes: how a period splits
const (
	TermSchemeBimester  = "bimester"
	TermSchemeTrimester = "trimester"
	TermSchemeSemester  = "semester"
	TermSchemeNone      = "none"
)

// Grading scales
const (
	ScaleLiteral   = "literal"
	ScaleVigesimal = "vigesimal"
	ScaleScore     = "score"
)

// Period kinds and statuses
const (
	PeriodSchoolYear  = "school_year"
	PeriodPromotional = "promotional"
	PeriodCycle       = "cycle"
	PeriodSemester    = "semester"

	PeriodPlanning = "planning"
	PeriodActive   = "active"
	PeriodClosed   = "closed"

	TermOpen   = "open"
	TermClosed = "closed"
)

// AcademicSettings is the academic profile of one institution (academic.settings)
type AcademicSettings struct {
	TermScheme     string
	GradingScale   string
	PassingGrade   *int
	ConductGrade   bool
	AttentionModes []string
	Shifts         []string
	// Teaching days, 1 = Monday … 7 = Sunday
	WeekDays []int
	// Minutes after the first class that still count as on time, and the absence share that raises an alert
	LateAfterMinutes    int
	AbsenceLimitPercent int
	// Official data for the report card: regional office (DRE/GRE), UGEL, modular code and the logo (image data URL)
	Dre         string
	Ugel        string
	ModularCode string
	Logo        string
	// Tipo de gestión: public (no tuition, only enrollment and the APAFA fee) | private
	Management string
	UpdatedAt  time.Time
}

// Management types of an institution (academic.settings.management)
const (
	ManagementPublic  = "public"
	ManagementPrivate = "private"
)

// Grade is a grade, age or plan semester inside a level
type Grade struct {
	ID        string
	LevelID   string
	Code      string
	Name      string
	ShortName string
	Position  int
	// Sections across every period; a grade with sections cannot be deleted
	SectionCount int
}

// Level is the first level of the structure: level, EBA cycle, CEPRE area or IEST study program
type Level struct {
	ID             string
	InstitutionID  string
	Code           string
	Name           string
	Description    string
	Position       int
	FormativeLevel string
	Credits        *int
	AttentionModes []string
	IsActive       bool
	Grades         []Grade
	SectionCount   int
}

// Term is a bimester, trimester or semester of a period
type Term struct {
	ID       string
	PeriodID string
	Number   int
	Name     string
	StartsOn time.Time
	EndsOn   time.Time
	Status   string
}

// Period is a school year, EBA promotional period, CEPRE cycle or IEST semester
type Period struct {
	ID            string
	InstitutionID string
	Kind          string
	Name          string
	Season        string
	StartsOn      time.Time
	EndsOn        time.Time
	TermScheme    string
	Status        string
	Terms         []Term
	SectionCount  int
	CapacityTotal int
}

// EBA programs and the variants of its distance form (academic.sections.program / distance_variant)
const (
	EBAProgramPEBAJA   = "pebaja"
	EBAProgramLiteracy = "literacy"

	DistanceVirtual   = "virtual"
	DistanceItinerant = "itinerant"
)

// EBAProgramMinAge is the youngest a student may enroll at in each EBA program: PEBAJA from 14, Alfabetización
// from 15 (Programa Curricular EBA, §1.3)
var EBAProgramMinAge = map[string]int{EBAProgramPEBAJA: 14, EBAProgramLiteracy: 15}

// Section is a group of students of one grade (or CEPRE area) in a period
type Section struct {
	ID            string
	PeriodID      string
	LevelID       string
	LevelName     string
	GradeID       string
	GradeName     string
	GradeShort    string
	Name          string
	Shift         string
	AttentionMode string
	// EBA only: pebaja | literacy, and virtual | itinerant when the form is distance
	Program         string
	DistanceVariant string
	// EBA only: the section gathers students of several grades of its cycle (GradeID is the lowest)
	Multigrade bool
	Capacity   int
	Room       string
	// Position of its level and grade, for ordering
	LevelPosition int
	GradePosition int
	TutorID       string
	TutorName     string
}

// SectionBatch creates one section per grade × name (CEPRE: per name, no grade)
type SectionBatch struct {
	LevelID         string
	GradeIDs        []string
	Names           []string
	Shift           string
	AttentionMode   string
	Program         string
	DistanceVariant string
	Multigrade      bool
	Capacity        int
	Room            string
}

// LevelInput and GradeInput are what the panel sends to create or edit
type LevelInput struct {
	Code           string
	Name           string
	Description    string
	FormativeLevel string
	Credits        *int
	AttentionModes []string
	IsActive       bool
}

type GradeInput struct {
	Code      string
	Name      string
	ShortName string
}

type PeriodInput struct {
	Name       string
	Season     string
	StartsOn   time.Time
	EndsOn     time.Time
	TermScheme string
}

type TermInput struct {
	Name     string
	StartsOn time.Time
	EndsOn   time.Time
	Status   string
}

type SectionInput struct {
	Name            string
	Shift           string
	AttentionMode   string
	Program         string
	DistanceVariant string
	Multigrade      bool
	Capacity        int
	Room            string
}

var (
	ErrAcademicForbidden = newError(KindForbidden, "academic_forbidden", "Tu rol no puede ver la estructura académica.")
	ErrAcademicReadOnly  = newError(KindForbidden, "academic_read_only", "Solo dirección y coordinación académica pueden hacer cambios aquí.")
	ErrLevelInUse        = newError(KindConflict, "level_in_use", "No se puede eliminar: tiene grados o secciones. Elimínalos primero o desactívalo.")
	ErrGradeInUse        = newError(KindConflict, "grade_in_use", "No se puede eliminar: tiene secciones en algún periodo.")
	ErrPeriodInUse       = newError(KindConflict, "period_in_use", "Solo se pueden eliminar periodos en planificación.")
	ErrPeriodClosed      = newError(KindConflict, "period_closed", "El periodo está cerrado. Reábrelo para hacer cambios.")
	ErrActivePeriodTaken = newError(KindConflict, "active_period_taken", "Ya hay un periodo en curso. Ciérralo antes de activar otro.")
	ErrStructureNotEmpty = newError(KindConflict, "structure_not_empty", "La plantilla solo se carga cuando la estructura está vacía.")
	ErrDuplicateName     = newError(KindConflict, "duplicate_name", "Ya existe uno con ese nombre o código.")
)
