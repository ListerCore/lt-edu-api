package domain

import "time"

// WellStudent is a student as the wellbeing screens list them, with what is open in their file
type WellStudent struct {
	ID           string
	Code         string
	Name         string
	Placement    string
	SectionID    string
	LevelID      string
	SpecialNeeds bool
	// Open matters (counted for the list)
	PendingCommitments int
	OpenIncidents      int
	ActiveCase         bool
	HasPlan            bool
	LastInterview      *time.Time
}

type Interview struct {
	ID          string
	StudentID   string
	StudentName string
	Kind        string
	HeldOn      time.Time
	Topic       string
	Notes       string
	Agreements  string
	AuthorID    string
	AuthorName  string
}

type Commitment struct {
	ID          string
	StudentID   string
	StudentName string
	Placement   string
	InterviewID string
	Party       string
	Description string
	DueOn       time.Time
	Status      string
	CheckedOn   *time.Time
	AuthorName  string
}

// PsychCase is a psychology case. Reason and sessions are confidential: only psychologists read them
type PsychCase struct {
	ID               string
	StudentID        string
	StudentName      string
	Placement        string
	Origin           string
	ReferredBy       string
	ReferredName     string
	Reason           string
	Priority         string
	Status           string
	OpenedOn         time.Time
	ClosedOn         *time.Time
	PsychologistName string
	SharedNote       string
	Sessions         int
	LastSession      *time.Time
}

type PsychSession struct {
	ID         string
	CaseID     string
	HeldOn     time.Time
	Kind       string
	Notes      string
	NextSteps  string
	AuthorName string
}

type Incident struct {
	ID               string
	StudentID        string
	StudentName      string
	Placement        string
	OccurredOn       time.Time
	Severity         string
	Category         string
	Description      string
	ReportedBy       string
	ReportedName     string
	FamilyNotifiedOn *time.Time
	Status           string
	Measures         []Measure
	// Reported by the person asking (set by the service)
	Mine bool
}

type Measure struct {
	ID                    string
	StudentID             string
	IncidentID            string
	Kind                  string
	Ground                string
	Detail                string
	StartsOn              time.Time
	Days                  int
	DecidedName           string
	NotifiedOn            *time.Time
	ReconsiderationOn     *time.Time
	ReconsiderationReason string
	ReconsiderationBy     string
	ResolvedOn            *time.Time
	Outcome               string
	Resolution            string
}

// Counts reports whether the measure still stands (a revoked one does not count towards the next)
func (m Measure) Counts() bool { return m.Outcome != "revoked" }

// IncidentFilter narrows the discipline register
type IncidentFilter struct {
	InstitutionID string
	StudentIDs    []string
	From, To      *time.Time
	Severity      string
	Status        string
}

// IndividualPlan is the POI (Plan de orientación individual) of a special education student for a period
type IndividualPlan struct {
	ID                    string
	StudentID             string
	StudentName           string
	Placement             string
	PeriodID              string
	PeriodName            string
	SpecialistID          string
	SpecialistName        string
	Needs                 string
	Strengths             string
	EducationalResponse   string
	CurricularAdaptations string
	AccessAdaptations     string
	EvaluationCriteria    string
	Actors                string
	FamilyCommitments     string
	Status                string
	ApprovedName          string
	ApprovedOn            *time.Time
	UpdatedAt             time.Time
	Therapies             int
}

type TherapySession struct {
	ID            string
	StudentID     string
	StudentName   string
	TherapistID   string
	TherapistName string
	Kind          string
	HeldOn        time.Time
	Minutes       int
	Attended      bool
	Goal          string
	Notes         string
	HomeTasks     string
	// Logged by the person asking (set by the service)
	Mine bool
}

// TermConduct is the 0–20 conduct grade of a term (assessment.conduct_marks)
type TermConduct struct {
	TermID   string
	TermName string
	Score    int
	Note     string
}

// WellAlert is one line of the alerts panel
type WellAlert struct {
	Kind        string
	Level       string
	StudentID   string
	StudentName string
	Placement   string
	Title       string
	Detail      string
	Since       *time.Time
	// The precise signal (dropout: inactive | overdue | low_permanence; commitment), to act on it
	Code string
}

var (
	ErrWellbeingForbidden  = newError(KindForbidden, "wellbeing_forbidden", "Tu rol no puede ver bienestar.")
	ErrWellbeingReadOnly   = newError(KindForbidden, "wellbeing_read_only", "Tu rol solo puede consultar esta información.")
	ErrNotYourTutee        = newError(KindForbidden, "not_your_tutee", "El estudiante no es de tu sección de tutoría.")
	ErrPsychConfidential   = newError(KindForbidden, "psych_confidential", "Los casos de psicología son confidenciales: solo los ve el equipo de psicología.")
	ErrDirectorMeasure     = newError(KindForbidden, "director_measure", "Las faltas graves y muy graves, las suspensiones, la matrícula condicional y la expulsión las decide la dirección.")
	ErrMeasureGround       = newError(KindConflict, "measure_ground", "No se cumple la causal que señala la norma para esa medida.")
	ErrReconsiderationLate = newError(KindConflict, "reconsideration_late", "El plazo para pedir reconsideración (3 días hábiles desde la comunicación) ya venció.")
	ErrReconsiderationOpen = newError(KindConflict, "reconsideration_open", "Ya hay un pedido de reconsideración para esta medida.")
	ErrNotNotified         = newError(KindConflict, "not_notified", "Primero registra cuándo se comunicó la medida a la familia.")
	ErrNotSpecialEd        = newError(KindInvalid, "not_special_ed", "El plan de orientación individual y las terapias son de educación básica especial.")
	ErrNotYourPlan         = newError(KindForbidden, "not_your_plan", "No eres el especialista a cargo de ese estudiante.")
	ErrPlanApproved        = newError(KindConflict, "plan_approved", "El plan ya fue aprobado: pide a coordinación que lo reabra.")
)
