package service

import (
	"strconv"

	"api/internal/core/domain"
)

// AcademicVocabulary is how the panel names the structure of one institution kind
type AcademicVocabulary struct {
	Level, Levels     string
	Grade, Grades     string
	Section, Sections string
	Period, Periods   string
}

// academicProfile holds the rules of one institution kind (docs/plan-academico.md §4)
type academicProfile struct {
	vocabulary AcademicVocabulary
	periodKind string
	// Term schemes the kind may use; the first is the default
	termSchemes []string
	scale       string
	passing     *int
	conduct     bool
	// Sections hang from a grade (false: CEPRE areas have no grades)
	hasGrades bool
	// EBA forms of attention; CEPRE seasons; IEST formative levels and credits
	usesAttentionModes  bool
	usesSeasons         bool
	usesFormativeLevels bool
	// Only one period may be running at a time (school year, institute semester); EBA and CEPRE overlap them
	singleActivePeriod bool
	defaultShifts      []string
	// Teaching days by default (academies and institutes also teach on Saturday)
	weekDays []int
	template []domain.Level
}

var (
	thirteen        = 13
	allModes        = []string{"in_person", "blended", "distance"}
	eduBasicSchemes = []string{domain.TermSchemeBimester, domain.TermSchemeTrimester, domain.TermSchemeSemester}

	academicProfiles = map[string]academicProfile{
		domain.KindEBR: {
			vocabulary:  AcademicVocabulary{"Nivel", "Niveles", "Grado", "Grados", "Sección", "Secciones", "Año lectivo", "Años lectivos"},
			periodKind:  domain.PeriodSchoolYear,
			termSchemes: eduBasicSchemes, scale: domain.ScaleLiteral, conduct: true, hasGrades: true, singleActivePeriod: true,
			defaultShifts: []string{"morning", "afternoon"},
			template: []domain.Level{
				level("inicial", "Inicial", "Ciclo II: niños de 3 a 5 años.", ages(3, 5)),
				level("primaria", "Primaria", "Ciclos III, IV y V.", ordinals(6, "grado")),
				level("secundaria", "Secundaria", "Ciclos VI y VII.", ordinals(5, "año")),
			},
		},
		domain.KindEBA: {
			vocabulary:  AcademicVocabulary{"Ciclo", "Ciclos", "Grado", "Grados", "Sección", "Secciones", "Periodo promocional", "Periodos promocionales"},
			periodKind:  domain.PeriodPromotional,
			termSchemes: eduBasicSchemes, scale: domain.ScaleLiteral, hasGrades: true, usesAttentionModes: true,
			defaultShifts: []string{"afternoon", "evening"},
			template: []domain.Level{
				withModes(level("inicial", "Ciclo Inicial", "Alfabetización. Solo presencial.", ordinals(2, "grado")), "in_person"),
				withModes(level("intermedio", "Ciclo Intermedio", "Equivale a la primaria. Presencial o semipresencial.", ordinals(3, "grado")), "in_person", "blended"),
				withModes(level("avanzado", "Ciclo Avanzado", "Equivale a la secundaria. Presencial, semipresencial o a distancia.", ordinals(4, "grado")), allModes...),
			},
		},
		domain.KindEBE: {
			vocabulary:  AcademicVocabulary{"Nivel", "Niveles", "Grado o edad", "Grados y edades", "Aula", "Aulas", "Año lectivo", "Años lectivos"},
			periodKind:  domain.PeriodSchoolYear,
			termSchemes: []string{domain.TermSchemeTrimester, domain.TermSchemeBimester, domain.TermSchemeSemester}, scale: domain.ScaleLiteral, hasGrades: true, singleActivePeriod: true,
			defaultShifts: []string{"morning"},
			template: []domain.Level{
				level("prite", "PRITE", "Intervención temprana de 0 a 3 años, con la familia.", []domain.Grade{
					grade("0-1", "0 a 1 año", "0-1"), grade("1-2", "1 a 2 años", "1-2"), grade("2-3", "2 a 3 años", "2-3"),
				}),
				level("inicial", "Inicial", "De 3 a 5 años.", ages(3, 5)),
				level("primaria", "Primaria", "Hasta los 20 años en casos de discapacidad severa.", ordinals(6, "grado")),
			},
		},
		domain.KindCEPRE: {
			vocabulary:  AcademicVocabulary{"Área", "Áreas", "", "", "Aula", "Aulas", "Ciclo", "Ciclos"},
			periodKind:  domain.PeriodCycle,
			termSchemes: []string{domain.TermSchemeNone}, scale: domain.ScaleScore, usesSeasons: true,
			defaultShifts: []string{"morning", "afternoon", "evening"}, weekDays: []int{1, 2, 3, 4, 5, 6},
			template: []domain.Level{
				level("uni", "UNI · Ingenierías", "Universidad Nacional de Ingeniería: matemática y física con mayor peso.", nil),
				level("unmsm-a", "San Marcos · Área A", "Ciencias de la Salud.", nil),
				level("unmsm-b", "San Marcos · Área B", "Ciencias Básicas.", nil),
				level("unmsm-c", "San Marcos · Área C", "Ingenierías.", nil),
				level("unmsm-d", "San Marcos · Área D", "Ciencias Económicas y de la Gestión.", nil),
				level("unmsm-e", "San Marcos · Área E", "Humanidades y Ciencias Jurídicas y Sociales.", nil),
			},
		},
		domain.KindInstitute: {
			vocabulary:  AcademicVocabulary{"Programa de estudios", "Programas de estudios", "Semestre", "Semestres", "Sección", "Secciones", "Semestre académico", "Semestres académicos"},
			periodKind:  domain.PeriodSemester,
			termSchemes: []string{domain.TermSchemeNone}, scale: domain.ScaleVigesimal, passing: &thirteen, hasGrades: true, usesFormativeLevels: true, singleActivePeriod: true,
			defaultShifts: []string{"morning", "evening"}, weekDays: []int{1, 2, 3, 4, 5, 6},
			template: []domain.Level{
				program("enfermeria-tecnica", "Enfermería Técnica", "Cuidado del paciente en hospitales y centros de salud."),
				program("desarrollo-sistemas", "Desarrollo de Sistemas de Información", "Programación, bases de datos y soporte de aplicaciones."),
				program("contabilidad", "Contabilidad", "Contabilidad, tributación y gestión financiera."),
			},
		},
	}
)

func profileFor(kind string) academicProfile {
	if p, ok := academicProfiles[kind]; ok {
		return p
	}
	return academicProfiles[domain.KindEBR]
}

func level(code, name, description string, grades []domain.Grade) domain.Level {
	return domain.Level{Code: code, Name: name, Description: description, IsActive: true, Grades: grades, AttentionModes: []string{}}
}

func withModes(l domain.Level, modes ...string) domain.Level {
	l.AttentionModes = modes
	return l
}

// program is an IEST profesional técnico plan: 120 credits over six semesters
func program(code, name, description string) domain.Level {
	credits := 120
	l := level(code, name, description, nil)
	l.FormativeLevel, l.Credits = "professional_technical", &credits
	for i := 1; i <= 6; i++ {
		r := roman(i)
		l.Grades = append(l.Grades, grade(lowerASCII(r), "Semestre "+r, r))
	}
	return l
}

func grade(code, name, short string) domain.Grade {
	return domain.Grade{Code: code, Name: name, ShortName: short}
}

// ordinals builds "1.° grado"… or "1.° año"…
func ordinals(n int, noun string) []domain.Grade {
	out := make([]domain.Grade, n)
	for i := range out {
		num := strconv.Itoa(i + 1)
		out[i] = grade(num, num+".° "+noun, num+".°")
	}
	return out
}

func ages(from, to int) []domain.Grade {
	var out []domain.Grade
	for a := from; a <= to; a++ {
		num := strconv.Itoa(a)
		out = append(out, grade(num+"-anos", num+" años", num+" a"))
	}
	return out
}

var romans = []string{"I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X"}

func roman(n int) string {
	if n >= 1 && n <= len(romans) {
		return romans[n-1]
	}
	return strconv.Itoa(n)
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
