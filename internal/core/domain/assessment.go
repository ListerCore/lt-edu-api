package domain

import "time"

// Achievement levels of Educación Básica (RVM 094-2020-MINEDU), best first
var Levels = []string{"AD", "A", "B", "C"}

// Sheet kinds: levels per competency (EB), scores 0–20 per evaluation (IEST, CEPRE) or the tutor's sheet
// (transversal competencies, conduct and the comment to the family)
const (
	SheetLiteral = "literal"
	SheetNumeric = "numeric"
	SheetTutor   = "tutor"
)

// SectionMeta is a section as the grade sheets show it
type SectionMeta struct {
	ID    string
	Label string
	// The grade ("3.° grado") and the section's own name ("A"), for official documents
	GradeName   string
	SectionName string
	LevelName   string
	PeriodID    string
	TutorID     string
	TutorName   string
}

// StudentRef is a student of a sheet, ordered by surnames like the official register
type StudentRef struct {
	ID       string
	Code     string
	Name     string
	Document string
}

// CompetencyRef is a competency column of a sheet (Transversal: no area, marked by the tutor)
type CompetencyRef struct {
	ID          string
	SubjectID   string
	Code        string
	Name        string
	Transversal bool
}

type CompetencyMark struct {
	StudentID    string
	CompetencyID string
	TermID       string
	Level        string
	Conclusion   string
}

// Evaluation is a graded activity of a unit or course in a term, with its weight in the average
type Evaluation struct {
	ID           string
	AssignmentID string
	TermID       string
	Name         string
	Weight       int
	HeldOn       *time.Time
	Position     int
}

type Score struct {
	EvaluationID string
	StudentID    string
	Score        int
}

type ConductMark struct {
	StudentID string
	TermID    string
	Score     int
	Note      string
}

type TutorComment struct {
	StudentID string
	TermID    string
	Comment   string
}

// Submission is a sheet the teacher sent to coordinación (AssignmentID empty: the tutor's sheet)
type Submission struct {
	SectionID     string
	AssignmentID  string
	TermID        string
	SubmittedName string
	SubmittedAt   time.Time
}

// SheetProgress is how complete one sheet is, for coordinación's overview
// TutorCounts is what the gradebook shows of a tutor's register in one section and term, counted by the database
type TutorCounts struct {
	SectionID   string
	Label       string
	LevelName   string
	TutorName   string
	Students    int
	Transversal int
	// Transversal competency levels filled for enrolled students
	Filled int
	// Conduct marks of enrolled students
	ConductFilled int
}

type SheetProgress struct {
	SectionID    string
	SectionLabel string
	AssignmentID string
	SubjectID    string
	SubjectName  string
	StaffID      string
	StaffName    string
	Students     int
	// Cells to fill (students × competencies, or students × evaluations) and the ones filled
	Expected    int
	Filled      int
	Evaluations int
	Submitted   *Submission
}

var (
	ErrGradesForbidden = newError(KindForbidden, "grades_forbidden", "Tu rol no puede ver las notas.")
	ErrNotYourSheet    = newError(KindForbidden, "not_your_sheet", "Solo el docente del curso o coordinación pueden calificar aquí.")
	ErrTermClosed      = newError(KindConflict, "term_closed", "El periodo de evaluación está cerrado: las notas ya no se cambian.")
	ErrWrongSheetKind  = newError(KindInvalid, "wrong_sheet_kind", "Este curso no se califica de esa forma.")
	ErrNoConduct       = newError(KindInvalid, "no_conduct", "La institución no usa nota de conducta.")
	ErrCardNotReady    = newError(KindForbidden, "card_not_ready", "La libreta estará disponible cuando se cierre el periodo de evaluación.")
)
