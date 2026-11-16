package domain

import "time"

// FinalResult is the stored final situation of a student in a period (Educación Básica)
type FinalResult struct {
	StudentID     string
	SectionID     string
	Computed      string
	Reason        string
	Decided       string
	DecisionNote  string
	DecidedName   string
	DecidedAt     *time.Time
	ConfirmedAt   *time.Time
	AfterRecovery string
	RecoveryAt    *time.Time
}

// Situation is the one in force: the team's decision over the rules
func (r FinalResult) Situation() string {
	if r.Decided != "" {
		return r.Decided
	}
	return r.Computed
}

// UnitResult is the confirmed result of a student in an IEST unit (or a course with scores)
type UnitResult struct {
	StudentID     string
	Average       *float64
	Grade         *int
	AbsentPercent float64
	Status        string
	RecoveryScore *int
	FinalGrade    *int
	ConfirmedAt   time.Time
}

// Record is an issued acta; Content is the frozen JSON the app prints
type Record struct {
	ID           string
	PeriodID     string
	SectionID    string
	SectionLabel string
	AssignmentID string
	Kind         string
	Number       int
	Code         string
	Status       string
	Content      []byte
	IssuedName   string
	IssuedAt     time.Time
	// Phase E7: the signed version of the acta (documents), empty until generated
	DocumentID   string
	ActVersionID string
	ActStatus    string
}

// SectionLevel is where a section sits, to pick its promotion rule
type SectionLevel struct {
	LevelCode  string
	LevelName  string
	GradeCode  string
	GradeShort string
}

var (
	ErrClosingForbidden  = newError(KindForbidden, "closing_forbidden", "Solo dirección y coordinación hacen el cierre del año.")
	ErrLastTermOpen      = newError(KindConflict, "last_term_open", "Primero cierra el último periodo de evaluación (en Periodos).")
	ErrPendingLevels     = newError(KindConflict, "pending_levels", "Hay estudiantes con niveles finales pendientes: complétalos o registra la decisión del equipo.")
	ErrNotConfirmed      = newError(KindConflict, "not_confirmed", "Primero confirma los resultados de la sección.")
	ErrNotInRecovery     = newError(KindConflict, "not_in_recovery", "El estudiante no está en recuperación.")
	ErrNothingToRecord   = newError(KindConflict, "nothing_to_record", "No hay estudiantes para esta acta.")
	ErrNoClosingForKind  = newError(KindInvalid, "no_closing", "Las academias no tienen promoción: el ciclo termina con el reporte de avance.")
	ErrUnitNotConfirmed  = newError(KindConflict, "unit_not_confirmed", "Primero confirma los resultados de la unidad.")
	ErrNotUnitRecovery   = newError(KindConflict, "not_unit_recovery", "Ese estudiante no está para recuperación en esta unidad.")
	ErrWrongClosingScale = newError(KindInvalid, "wrong_closing_scale", "Este cierre no corresponde a la escala de la institución.")
)
