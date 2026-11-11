package domain

import "time"

// Plan statuses
const (
	CurriculumDraft    = "draft"
	CurriculumActive   = "active"
	CurriculumArchived = "archived"
)

// Subject kinds and institute components
const (
	SubjectArea     = "area"
	SubjectWorkshop = "workshop"
	SubjectTutoring = "tutoring"
	SubjectCourse   = "course"
	SubjectUnit     = "unit"
	SubjectEFSRT    = "efsrt"

	ComponentSpecific      = "specific"
	ComponentEmployability = "employability"
	ComponentEFSRT         = "efsrt"
)

// Competency kinds: CNEB competencies (Educación Básica), capacities (institute units), topics (CEPRE courses)
const (
	CompetencyKind = "competency"
	CapacityKind   = "capacity"
	TopicKind      = "topic"
)

// Competency is what is assessed in a subject; SubjectID empty means transversal (CNEB 30 and 31)
type Competency struct {
	ID        string
	SubjectID string
	Kind      string
	Code      string
	Name      string
	Position  int
}

// Subject is an area, workshop, course, didactic unit or EFSRT of a plan
type Subject struct {
	ID              string
	CurriculumID    string
	ModuleID        string
	Kind            string
	Component       string
	Code            string
	Name            string
	Description     string
	WeeklyHours     *int
	TheoryCredits   *int
	PracticeCredits *int
	Position        int
	GradeIDs        []string
	Competencies    []Competency
}

// Module is an IEST formative module
type Module struct {
	ID          string
	Code        string
	Name        string
	Description string
	Position    int
}

// Curriculum is a plan of studies of one level, with what it teaches
type Curriculum struct {
	ID            string
	InstitutionID string
	LevelID       string
	LevelName     string
	Name          string
	Notes         string
	Status        string
	ActivatedAt   *time.Time
	UpdatedAt     time.Time
	Modules       []Module
	Subjects      []Subject
	// Transversal competencies (not tied to a subject)
	Transversal []Competency
	// Summary for lists
	SubjectCount int
	TotalCredits int
}

type CurriculumInput struct {
	Name  string
	Notes string
}

type ModuleInput struct {
	Code        string
	Name        string
	Description string
}

type SubjectInput struct {
	ModuleID        string
	Kind            string
	Component       string
	Code            string
	Name            string
	Description     string
	WeeklyHours     *int
	TheoryCredits   *int
	PracticeCredits *int
	GradeIDs        []string
}

type CompetencyInput struct {
	SubjectID string
	Code      string
	Name      string
}

var (
	ErrCurriculumArchived = newError(KindConflict, "curriculum_archived", "Este plan está archivado. Crea una nueva versión para hacer cambios.")
	ErrCurriculumNotDraft = newError(KindConflict, "curriculum_not_draft", "Solo se pueden eliminar planes en borrador.")
)
