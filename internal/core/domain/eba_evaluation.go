package domain

import "time"

// Kinds of document of phase E7, signed through documents
const (
	DocumentEvaluationRecord = "evaluation_record"
	DocumentCertificate      = "certificate"
)

// Certificate kinds: enrollment in a period, studies done (every period with results) and conclusion of a cycle
const (
	CertificateEnrollment = "enrollment"
	CertificateStudies    = "studies"
	CertificateCycle      = "cycle"
)

// LevelRule is the promotion rule a CEBA set for one cycle (Custom false: the default of the code)
type LevelRule struct {
	LevelID     string
	LevelCode   string
	LevelName   string
	Automatic   bool
	Areas       int
	Note        string
	Custom      bool
	UpdatedName string
	UpdatedAt   *time.Time
}

// ClosingStudent is what the closing needs of a student beyond the marks: the grade they are enrolled in (their own
// one in a multigrade section), the Religion exemption and the SIAGIE state of the grades
type ClosingStudent struct {
	EnrollmentID   string
	GradeName      string
	GradeShort     string
	ReligionExempt bool
	SiagieStatus   string
	SiagieNote     string
	SiagieAt       *time.Time
}

// StudyRecord is one period a student studied with the final situation in force (after recovery when there was one)
type StudyRecord struct {
	EnrollmentID string
	PeriodID     string
	PeriodName   string
	Year         int
	LevelID      string
	LevelName    string
	GradeName    string
	GradeShort   string
	// Last grade of its cycle: promotion there concludes the cycle
	LastGrade     bool
	SectionName   string
	AttentionMode string
	Status        string
	Situation     string
	Confirmed     bool
}

// StudentCertificate is an issued constancia
type StudentCertificate struct {
	ID           string
	StudentID    string
	StudentName  string
	Kind         string
	PeriodID     string
	PeriodName   string
	LevelID      string
	LevelName    string
	Purpose      string
	Year         int
	Number       int
	DocumentID   string
	VersionID    string
	VersionState string
	CreatedName  string
	CreatedAt    time.Time
}

type StudentCertificateInput struct {
	StudentID string
	Kind      string
	PeriodID  string
	LevelID   string
	Purpose   string
}

var (
	ErrRecordNotFinal     = newError(KindInvalid, "record_not_final", "Solo se firman las actas consolidadas y las de recuperación.")
	ErrRecordReplaced     = newError(KindConflict, "record_replaced", "Esa acta fue reemplazada: firma la emisión vigente.")
	ErrNotEnrolledThen    = newError(KindConflict, "not_enrolled_then", "El estudiante no tiene matrícula en ese periodo.")
	ErrNoStudies          = newError(KindConflict, "no_studies", "El estudiante no tiene estudios registrados en el CEBA.")
	ErrCycleNotConcluded  = newError(KindConflict, "cycle_not_concluded", "El estudiante no ha concluido ese ciclo: le falta la promoción del último grado.")
	ErrNoCertificateKind  = newError(KindInvalid, "certificate_kind", "Elige el tipo de constancia.")
	ErrSiagieNoteRequired = newError(KindInvalid, "siagie_note", "Explica la observación del SIAGIE.")
)
