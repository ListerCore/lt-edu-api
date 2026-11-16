package domain

import "time"

// EBA enrollment (phase E1): the ficha única de matrícula, the exemption from Educación Religiosa, the SIAGIE upload
// state, the nómina and the placement procedures with the acta the direction signs

const (
	DocumentPlacement = "placement"

	PlacementTest         = "placement_test"
	PlacementValidation   = "validation"
	PlacementRevalidation = "revalidation"
	PlacementEquivalence  = "equivalence"

	SiagiePending  = "pending"
	SiagieExported = "exported"
	SiagieObserved = "observed"

	// Minimum age to enroll in each EBA program (DS 011-2012-ED, Reglamento de la Ley General de Educación)
	MinAgePEBAJA   = 14
	MinAgeLiteracy = 15

	// Code of the area a student may be exempted from (Ley 29635, libertad religiosa)
	ReligionSubjectCode = "educacion-religiosa"
)

// EnrollmentFile is what the ficha única asks beyond the student record; kept across periods
type EnrollmentFile struct {
	StudentID         string
	MotherTongue      string
	MotherTongueOther string
	Occupation        string
	Disability        string
	Internet          string
	Device            string
	PreferredMode     string
	LastStudies       string
	Documents         []string
	UpdatedByName     string
	UpdatedAt         *time.Time
}

// ReligionExemption of an enrollment, granted by directoral resolution
type ReligionExemption struct {
	EnrollmentID  string
	RequestedOn   time.Time
	Reference     string
	GrantedByName string
}

// RollRow is one enrollment of the nómina de matrícula and the SIAGIE export: the student, where they are enrolled,
// the exemption and the upload state
type RollRow struct {
	EnrollmentID    string
	StudentID       string
	StudentCode     string
	DocumentType    string
	DocumentNumber  string
	PaternalSurname string
	MaternalSurname string
	GivenNames      string
	Sex             string
	BirthDate       *time.Time
	SpecialNeeds    bool
	LevelID         string
	LevelCode       string
	LevelName       string
	GradeID         string
	GradeName       string
	GradePosition   int
	SectionID       string
	SectionName     string
	AttentionMode   string
	Program         string
	Kind            string
	Status          string
	EnrolledOn      time.Time
	EndedOn         *time.Time
	ReligionExempt  bool
	SiagieStatus    string
	SiagieNote      string
	SiagieExported  *time.Time
	MotherTongue    string
	// Placement procedure behind the grade, when any ("Convalidación, acta N.° 3-2026")
	Placement string
}

// RollFilter narrows the nómina: period (required), and optionally cycle, grade or section, and the SIAGIE state
type RollFilter struct {
	PeriodID     string
	LevelID      string
	GradeID      string
	SectionID    string
	SiagieStatus string
	// Active only (the default for the nómina); all includes withdrawn and transferred
	All bool
}

// AgeOn is the whole years of someone born on birth at day
func AgeOn(birth, day time.Time) int {
	age := day.Year() - birth.Year()
	if day.Month() < birth.Month() || (day.Month() == birth.Month() && day.Day() < birth.Day()) {
		age--
	}
	return age
}

// EBR studies a student brings, for the cuadro de equivalencias
const (
	OriginEBRPrimary   = "ebr_primaria"
	OriginEBRSecondary = "ebr_secundaria"
)

// EquivalentEntry proposes the EBA cycle (its code) and grade (0-based position) a student enters after passing
// `passed` grades of EBR primaria or secundaria, after the cuadro de equivalencias of the RVM 0018-2007-ED: ciclo
// inicial = 1.° and 2.° of primaria, intermedio = 3.°, 4.° and 5.°–6.°, avanzado = 1.°, 2.°, 3.° and 4.°–5.° of
// secundaria. done reports that the studies already complete the EBA (nothing to enter). It is a proposal: the
// direction decides with the evaluation
func EquivalentEntry(origin string, passed int) (level string, grade int, done, ok bool) {
	switch origin {
	case OriginEBRPrimary:
		switch {
		case passed <= 0:
			return "inicial", 0, false, true
		case passed == 1:
			return "inicial", 1, false, true
		case passed == 2:
			return "intermedio", 0, false, true
		case passed == 3:
			return "intermedio", 1, false, true
		case passed <= 5:
			return "intermedio", 2, false, true
		default:
			return "avanzado", 0, false, true
		}
	case OriginEBRSecondary:
		switch {
		case passed <= 0:
			return "avanzado", 0, false, true
		case passed <= 3:
			return "avanzado", passed, false, true
		case passed == 4:
			return "avanzado", 3, false, true
		default:
			return "", 0, true, true
		}
	}
	return "", 0, false, false
}

var (
	ErrUnderAge        = newError(KindInvalid, "under_age", "El estudiante no tiene la edad mínima del programa.")
	ErrPlacementSigned = newError(KindConflict, "placement_signed", "El acta ya está firmada: el trámite no se puede eliminar.")
	ErrNotExemptable   = newError(KindInvalid, "not_exemptable", "El plan de estudios de ese grado no tiene Educación Religiosa.")
	ErrNothingToExport = newError(KindConflict, "nothing_to_export", "No hay matrículas que exportar con ese filtro.")
)
