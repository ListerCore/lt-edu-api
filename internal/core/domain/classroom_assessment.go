package domain

import "time"

// Online assessments of the virtual classroom (classroom.assessments, questions, submissions, extensions)
const (
	ItemQuiz = "quiz"
	ItemTask = "task"

	QuestionSingle   = "single"
	QuestionMultiple = "multiple"
	QuestionShort    = "short"
	QuestionOpen     = "open"

	SubmissionInProgress = "in_progress"
	SubmissionSubmitted  = "submitted"
	SubmissionGraded     = "graded"

	OwnerSubmission = "submission"
)

// Assessment is a quiz or task of a unit: its window, attempts, the competencies it assesses and, for quizzes, the
// questions (Correct and Accepted are only filled for staff)
type Assessment struct {
	ItemID        string
	RoomID        string
	UnitID        string
	Kind          string
	Title         string
	Instructions  string
	Published     bool
	OpensAt       *time.Time
	ClosesAt      *time.Time
	MaxAttempts   int
	CompetencyIDs []string
	ShowAnswers   bool
	Questions     []QuizQuestion
}

// Open reports whether the assessment takes answers at t, given the student's extension (nil when none)
func (a Assessment) Open(t time.Time, extension *time.Time) bool {
	if a.OpensAt != nil && t.Before(*a.OpensAt) {
		return false
	}
	closes := a.ClosesAt
	if extension != nil && (closes == nil || extension.After(*closes)) {
		closes = extension
	}
	return closes == nil || !t.After(*closes)
}

type QuizQuestion struct {
	ID       string
	Position int
	Kind     string
	Prompt   string
	Options  []string
	Correct  []int
	Accepted []string
	Points   int
}

// QuizAnswer is what a student answered to one question
type QuizAnswer struct {
	Choices []int  `json:"choices,omitempty"`
	Text    string `json:"text,omitempty"`
}

// QuizSubmission is one attempt of a student
type QuizSubmission struct {
	ID          string
	ItemID      string
	StudentID   string
	StudentName string
	StudentCode string
	Attempt     int
	Status      string
	Answers     map[string]QuizAnswer
	AutoPoints  int
	AutoTotal   int
	OpenPoints  map[string]int
	Levels      map[string]string
	Feedback    string
	StartedAt   time.Time
	SubmittedAt *time.Time
	Late        bool
	GradedAt    *time.Time
	GradedName  string
	RecordedAt  *time.Time
	// Read-only: files handed in
	Files int
}

// QuizRosterRow is a student of the room with their latest attempt, if any, and their extension
type QuizRosterRow struct {
	StudentID   string
	StudentName string
	StudentCode string
	SectionID   string
	Latest      *QuizSubmission
	Attempts    int
	Extension   *time.Time
}

// CompetencyOption is a competency of the room's area an assessment may assess
type CompetencyOption struct {
	ID   string
	Name string
}

// RecordTarget is where a graded attempt goes in the official record: the student's section and the teaching
// assignment of the room's area there
type RecordTarget struct {
	SubmissionID string
	StudentID    string
	SectionID    string
	AssignmentID string
	Levels       map[string]string
	Feedback     string
}

var (
	ErrAssessmentClosed   = newError(KindConflict, "assessment_closed", "La evaluación no está abierta para recibir respuestas.")
	ErrNoAttemptsLeft     = newError(KindConflict, "no_attempts_left", "Ya usaste todos los intentos de esta evaluación.")
	ErrAttemptFinished    = newError(KindConflict, "attempt_finished", "Este intento ya fue entregado.")
	ErrQuestionsLocked    = newError(KindConflict, "questions_locked", "Ya hay entregas: las preguntas no se pueden cambiar. Puedes cambiar las fechas y los intentos.")
	ErrNoGradedLevels     = newError(KindConflict, "no_graded_levels", "No hay entregas calificadas con niveles para llevar al registro.")
	ErrNoOpenTerm         = newError(KindConflict, "no_open_term", "Hoy no hay un bimestre abierto en el periodo del aula.")
	ErrNotAnAssessment    = newError(KindInvalid, "not_an_assessment", "Ese material no es un cuestionario ni una tarea.")
	ErrSubmissionNotYours = newError(KindForbidden, "submission_not_yours", "Esta entrega no es tuya.")
)
