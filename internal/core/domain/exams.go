package domain

import "time"

// Course is a course of the academy's plans, identified by the code it shares across areas
type Course struct {
	Code   string
	Name   string
	Topics []string
	// Weekly hours per area (level id), from each area's plan
	Hours map[string]int
	// Questions in the bank
	Bank int
}

type Question struct {
	ID          string
	CourseCode  string
	Topic       string
	Stem        string
	Options     []string
	Answer      string
	Difficulty  string
	Explanation string
	AuthorName  string
	CreatedBy   string
	CreatedAt   time.Time
	// Mock exams that use it
	Uses int
}

type QuestionFilter struct {
	InstitutionID string
	CourseCode    string
	Topic         string
	Difficulty    string
	Query         string
	Limit         int
	Offset        int
}

type MockCourse struct {
	Code      string
	Name      string
	Questions int
	Position  int
}

type MockWeight struct {
	LevelID    string
	CourseCode string
	Weight     float64
}

// KeyItem is one question number of a mock exam: its course, the bank question (if any) and the right option
type KeyItem struct {
	Number     int
	CourseCode string
	QuestionID string
	Answer     string
}

type MockExam struct {
	ID          string
	PeriodID    string
	PeriodName  string
	Name        string
	HeldOn      time.Time
	Duration    int
	PointsRight float64
	PointsWrong float64
	Status      string
	Notes       string
	Courses     []MockCourse
	Weights     []MockWeight
	// Questions in total, answers in the key, and answer sheets loaded
	Questions int
	Keyed     int
	Sheets    int
}

// CourseScore is a student's result in one course of a mock exam
type CourseScore struct {
	Right int     `json:"right"`
	Wrong int     `json:"wrong"`
	Blank int     `json:"blank"`
	Score float64 `json:"score"`
}

type AnswerSheet struct {
	ID           string
	MockExamID   string
	StudentID    string
	StudentCode  string
	StudentName  string
	SectionID    string
	SectionLabel string
	LevelID      string
	LevelName    string
	Answers      string
	Source       string
	Score        float64
	Right        int
	Wrong        int
	Blank        int
	ByCourse     map[string]CourseScore
	// Ranks: in the classroom, the area and the whole exam (1 = best; ties share it)
	RankSection int
	RankLevel   int
	RankAll     int
}

// ExamStudent is a student of the cycle as the answer sheets place them
type ExamStudent struct {
	ID           string
	Code         string
	Document     string
	Name         string
	SectionID    string
	SectionLabel string
	LevelID      string
	LevelName    string
}

type Admission struct {
	ID          string
	StudentID   string
	StudentName string
	StudentCode string
	University  string
	Career      string
	Exam        string
	AdmittedOn  time.Time
	Place       *int
	Score       *float64
}

var (
	ErrExamsForbidden   = newError(KindForbidden, "exams_forbidden", "Tu rol no puede ver los simulacros.")
	ErrExamsReadOnly    = newError(KindForbidden, "exams_read_only", "Solo dirección y coordinación arman los simulacros.")
	ErrNotAnAcademy     = newError(KindInvalid, "not_an_academy", "Los simulacros son para academias preuniversitarias.")
	ErrNotYourQuestion  = newError(KindForbidden, "not_your_question", "Solo quien escribió la pregunta o coordinación pueden cambiarla.")
	ErrExamHasSheets    = newError(KindConflict, "exam_has_sheets", "El simulacro ya tiene respuestas cargadas: su estructura ya no se cambia.")
	ErrKeyIncomplete    = newError(KindConflict, "key_incomplete", "Completa la clave de respuestas antes de calificar.")
	ErrExamNotPublished = newError(KindForbidden, "exam_not_published", "Los resultados aún no se publican.")
)
