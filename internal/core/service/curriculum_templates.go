package service

import (
	"fmt"

	"api/internal/core/domain"
)

// Suggested study plans per institution kind and level code (the codes of academic_profiles.go templates). The
// panel loads them into a new plan, and the demo institutions start with them. Weekly hours are a reference for
// the regular full school day (JER); every institution adjusts them to its own PCI

// CNEB competencies by number (Currículo Nacional de la Educación Básica, RM 281-2016-MINEDU)
var cneb = map[int]string{
	1:  "Construye su identidad",
	2:  "Se desenvuelve de manera autónoma a través de su motricidad",
	3:  "Asume una vida saludable",
	4:  "Interactúa a través de sus habilidades sociomotrices",
	5:  "Aprecia de manera crítica manifestaciones artístico-culturales",
	6:  "Crea proyectos desde los lenguajes artísticos",
	7:  "Se comunica oralmente en su lengua materna",
	8:  "Lee diversos tipos de textos escritos en su lengua materna",
	9:  "Escribe diversos tipos de textos en su lengua materna",
	13: "Se comunica oralmente en inglés como lengua extranjera",
	14: "Lee diversos tipos de textos escritos en inglés como lengua extranjera",
	15: "Escribe diversos tipos de textos en inglés como lengua extranjera",
	16: "Convive y participa democráticamente en la búsqueda del bien común",
	17: "Construye interpretaciones históricas",
	18: "Gestiona responsablemente el espacio y el ambiente",
	19: "Gestiona responsablemente los recursos económicos",
	20: "Indaga mediante métodos científicos para construir sus conocimientos",
	21: "Explica el mundo físico basándose en conocimientos sobre los seres vivos, materia y energía, biodiversidad, Tierra y universo",
	22: "Diseña y construye soluciones tecnológicas para resolver problemas de su entorno",
	23: "Construye su identidad como persona humana, amada por Dios, digna, libre y trascendente, comprendiendo la doctrina de su propia religión, abierto al diálogo con las que le son cercanas",
	24: "Asume la experiencia del encuentro personal y comunitario con Dios en su proyecto de vida en coherencia con su creencia religiosa",
	25: "Resuelve problemas de cantidad",
	26: "Resuelve problemas de regularidad, equivalencia y cambio",
	27: "Resuelve problemas de forma, movimiento y localización",
	28: "Resuelve problemas de gestión de datos e incertidumbre",
	29: "Gestiona proyectos de emprendimiento económico o social",
	30: "Se desenvuelve en entornos virtuales generados por las TIC",
	31: "Gestiona su aprendizaje de manera autónoma",
}

type competencyTemplate struct {
	code, name string
}

type subjectTemplate struct {
	kind, component, code, name, description string
	hours                                    int // weekly hours; 0 = not set
	theory, practice                         int // institute credits
	grades                                   []string
	module                                   string
	competencies                             []competencyTemplate
}

type moduleTemplate struct {
	code, name, description string
}

type curriculumTemplate struct {
	name        string
	notes       string
	modules     []moduleTemplate
	subjects    []subjectTemplate
	transversal []competencyTemplate
}

func competenciesOf(numbers ...int) []competencyTemplate {
	out := make([]competencyTemplate, len(numbers))
	for i, n := range numbers {
		out[i] = competencyTemplate{code: fmt.Sprintf("C%d", n), name: cneb[n]}
	}
	return out
}

func area(code, name string, hours int, numbers ...int) subjectTemplate {
	return subjectTemplate{kind: domain.SubjectArea, code: code, name: name, hours: hours, competencies: competenciesOf(numbers...)}
}

func onlyIn(s subjectTemplate, grades ...string) subjectTemplate {
	s.grades = grades
	return s
}

func tutoring(hours int) subjectTemplate {
	return subjectTemplate{kind: domain.SubjectTutoring, code: "tutoria", name: "Tutoría y orientación educativa", description: "Acompañamiento socioafectivo y cognitivo de los estudiantes.", hours: hours}
}

var cnebTransversal = competenciesOf(30, 31)

const cnebNotes = "Áreas y competencias del Currículo Nacional de la Educación Básica. Las horas son referenciales; ajústalas a tu Proyecto Curricular Institucional."

var (
	ebrInicial = curriculumTemplate{
		name: "Plan CNEB", notes: cnebNotes, transversal: cnebTransversal,
		subjects: []subjectTemplate{
			area("personal-social", "Personal Social", 0, 1, 16, 23),
			area("psicomotriz", "Psicomotriz", 0, 2),
			area("comunicacion", "Comunicación", 0, 7, 8, 9, 6),
			area("matematica", "Matemática", 0, 25, 27),
			area("ciencia-tecnologia", "Ciencia y Tecnología", 0, 20),
		},
	}
	ebrPrimaria = curriculumTemplate{
		name: "Plan CNEB", notes: cnebNotes, transversal: cnebTransversal,
		subjects: []subjectTemplate{
			area("matematica", "Matemática", 5, 25, 26, 27, 28),
			area("comunicacion", "Comunicación", 5, 7, 8, 9),
			onlyIn(area("ingles", "Inglés como lengua extranjera", 2, 13, 14, 15), "3", "4", "5", "6"),
			area("personal-social", "Personal Social", 3, 1, 16, 17, 18, 19),
			area("ciencia-tecnologia", "Ciencia y Tecnología", 3, 20, 21, 22),
			area("arte-cultura", "Arte y Cultura", 3, 5, 6),
			area("educacion-fisica", "Educación Física", 3, 2, 3, 4),
			area("educacion-religiosa", "Educación Religiosa", 1, 23, 24),
			tutoring(2),
		},
	}
	ebrSecundaria = curriculumTemplate{
		name: "Plan CNEB", notes: cnebNotes, transversal: cnebTransversal,
		subjects: []subjectTemplate{
			area("matematica", "Matemática", 4, 25, 26, 27, 28),
			area("comunicacion", "Comunicación", 4, 7, 8, 9),
			area("ingles", "Inglés como lengua extranjera", 2, 13, 14, 15),
			area("arte-cultura", "Arte y Cultura", 2, 5, 6),
			area("ciencias-sociales", "Ciencias Sociales", 3, 17, 18, 19),
			area("dpcc", "Desarrollo Personal, Ciudadanía y Cívica", 3, 1, 16),
			area("educacion-fisica", "Educación Física", 2, 2, 3, 4),
			area("educacion-religiosa", "Educación Religiosa", 2, 23, 24),
			area("ciencia-tecnologia", "Ciencia y Tecnología", 3, 20, 21, 22),
			area("ept", "Educación para el Trabajo", 2, 29),
			tutoring(2),
		},
	}

	ebaNotes   = "Áreas del programa curricular de Educación Básica Alternativa con las competencias del CNEB. Las horas dependen de la forma de atención."
	ebaInicial = curriculumTemplate{
		name: "Plan EBA", notes: ebaNotes, transversal: cnebTransversal,
		subjects: []subjectTemplate{
			area("comunicacion", "Comunicación", 0, 7, 8, 9),
			area("matematica", "Matemática", 0, 25, 26, 27, 28),
			area("dpc", "Desarrollo Personal y Ciudadano", 0, 1, 16),
			area("cts", "Ciencia, Tecnología y Salud", 0, 20, 21),
		},
	}
	ebaIntermedio = curriculumTemplate{
		name: "Plan EBA", notes: ebaNotes, transversal: cnebTransversal,
		subjects: []subjectTemplate{
			area("comunicacion", "Comunicación", 0, 7, 8, 9),
			area("matematica", "Matemática", 0, 25, 26, 27, 28),
			area("dpc", "Desarrollo Personal y Ciudadano", 0, 1, 16, 17, 18, 19),
			area("cts", "Ciencia, Tecnología y Salud", 0, 20, 21, 22),
			area("ept", "Educación para el Trabajo", 0, 29),
			area("educacion-religiosa", "Educación Religiosa", 0, 23, 24),
		},
	}
	ebaAvanzado = curriculumTemplate{
		name: "Plan EBA", notes: ebaNotes, transversal: cnebTransversal,
		subjects: []subjectTemplate{
			area("comunicacion", "Comunicación", 0, 7, 8, 9),
			area("ingles", "Inglés", 0, 13, 14, 15),
			area("matematica", "Matemática", 0, 25, 26, 27, 28),
			area("dpc", "Desarrollo Personal y Ciudadano", 0, 1, 16, 17, 18, 19),
			area("cts", "Ciencia, Tecnología y Salud", 0, 20, 21, 22),
			area("ept", "Educación para el Trabajo", 0, 29),
			area("educacion-religiosa", "Educación Religiosa", 0, 23, 24),
		},
	}

	ebeNotes = "Áreas de desarrollo del CNEB para Educación Básica Especial. Cada estudiante las trabaja con las adaptaciones de su plan de orientación individual."
	ebePrite = curriculumTemplate{
		name: "Plan PRITE", notes: ebeNotes,
		subjects: []subjectTemplate{
			area("personal-social", "Personal Social", 0, 1, 16),
			area("psicomotriz", "Psicomotriz", 0, 2),
			area("comunicacion", "Comunicación", 0, 7),
			area("descubrimiento-mundo", "Descubrimiento del mundo", 0, 25, 27, 20),
		},
	}
)

// CEPRE: courses of the admission exam, with weekly hours weighted by area and the topics of each course
var cepreTopics = map[string][]string{
	"aritmetica":              {"Razones y proporciones", "Magnitudes proporcionales", "Tanto por ciento", "Teoría de conjuntos", "Divisibilidad y números primos"},
	"algebra":                 {"Leyes de exponentes", "Productos notables", "Ecuaciones e inecuaciones", "Funciones", "Logaritmos"},
	"geometria":               {"Triángulos", "Polígonos y cuadriláteros", "Circunferencia", "Áreas de regiones planas", "Geometría del espacio"},
	"trigonometria":           {"Razones trigonométricas", "Identidades trigonométricas", "Resolución de triángulos oblicuángulos"},
	"fisica":                  {"Cinemática", "Estática y dinámica", "Trabajo y energía", "Electricidad", "Óptica"},
	"quimica":                 {"Materia y energía", "Tabla periódica", "Enlace químico", "Estequiometría", "Química orgánica"},
	"biologia":                {"La célula", "Genética", "Fisiología humana", "Ecología"},
	"razonamiento-verbal":     {"Comprensión de lectura", "Analogías", "Sinónimos y antónimos", "Oraciones incompletas"},
	"razonamiento-matematico": {"Sucesiones", "Planteo de ecuaciones", "Conteo de figuras", "Razonamiento lógico"},
	"lenguaje":                {"Fonética y fonología", "Ortografía", "Morfología", "Sintaxis"},
	"literatura":              {"Géneros literarios", "Literatura peruana", "Literatura universal"},
	"historia-peru":           {"Perú prehispánico", "Virreinato", "Independencia", "República"},
	"economia":                {"Mercado", "Dinero e inflación", "Comercio internacional"},
}

func course(code, name string, hours int) subjectTemplate {
	s := subjectTemplate{kind: domain.SubjectCourse, code: code, name: name, hours: hours}
	for _, t := range cepreTopics[code] {
		s.competencies = append(s.competencies, competencyTemplate{name: t})
	}
	return s
}

func cepreUNI() curriculumTemplate {
	return curriculumTemplate{
		name: "Plan UNI", notes: "Cursos del examen de admisión de la UNI: matemática, física y química, aptitud académica y humanidades.",
		subjects: []subjectTemplate{
			course("aritmetica", "Aritmética", 4), course("algebra", "Álgebra", 4), course("geometria", "Geometría", 4),
			course("trigonometria", "Trigonometría", 3), course("fisica", "Física", 5), course("quimica", "Química", 4),
			course("razonamiento-matematico", "Razonamiento Matemático", 3), course("razonamiento-verbal", "Razonamiento Verbal", 3),
			course("humanidades", "Humanidades", 2),
		},
	}
}

// San Marcos: the same courses for every area, with more hours where the area's exam weighs more
func cepreSanMarcos(area string) curriculumTemplate {
	type c struct {
		code, name string
		hours      map[string]int // area → hours; "" is the default
	}
	courses := []c{
		{"habilidad-verbal", "Habilidad Verbal", map[string]int{"": 3, "e": 4}},
		{"habilidad-logico-matematica", "Habilidad Lógico-Matemática", map[string]int{"": 3}},
		{"aritmetica", "Aritmética", map[string]int{"": 2, "b": 3, "c": 4, "d": 3, "e": 1}},
		{"algebra", "Álgebra", map[string]int{"": 2, "b": 3, "c": 4, "d": 3, "e": 1}},
		{"geometria", "Geometría", map[string]int{"": 2, "b": 3, "c": 3, "d": 2, "e": 1}},
		{"trigonometria", "Trigonometría", map[string]int{"": 1, "b": 2, "c": 3}},
		{"lenguaje", "Lenguaje", map[string]int{"": 2, "e": 3}},
		{"literatura", "Literatura", map[string]int{"": 1, "e": 3}},
		{"psicologia", "Psicología", map[string]int{"": 1, "a": 2, "e": 2}},
		{"educacion-civica", "Educación Cívica", map[string]int{"": 1, "e": 2}},
		{"historia-peru", "Historia del Perú", map[string]int{"": 1, "d": 2, "e": 3}},
		{"historia-universal", "Historia Universal", map[string]int{"": 1, "e": 2}},
		{"geografia", "Geografía", map[string]int{"": 1, "d": 2, "e": 2}},
		{"economia", "Economía", map[string]int{"": 1, "d": 3, "e": 2}},
		{"filosofia", "Filosofía", map[string]int{"": 1, "e": 2}},
		{"fisica", "Física", map[string]int{"": 2, "a": 3, "b": 4, "c": 4, "e": 1}},
		{"quimica", "Química", map[string]int{"": 2, "a": 4, "b": 4, "c": 3, "e": 1}},
		{"biologia", "Biología", map[string]int{"": 2, "a": 4, "b": 3, "c": 1, "e": 1}},
	}
	t := curriculumTemplate{name: "Plan San Marcos", notes: "Cursos del examen de admisión de San Marcos, con más horas en los de mayor peso para esta área."}
	for _, x := range courses {
		h, ok := x.hours[area]
		if !ok {
			h = x.hours[""]
		}
		t.subjects = append(t.subjects, course(x.code, x.name, h))
	}
	return t
}

// Institutes: profesional técnico plans of 120 credits in six semesters and three formative modules, each closing
// with its EFSRT. Units: name, theory credits, practice credits
type unitRow struct {
	name             string
	theory, practice int
}

func instituteProgram(modules [3]moduleTemplate, semesters [6][]unitRow) curriculumTemplate {
	employability := [6][]unitRow{
		{{"Comunicación oral", 1, 1}, {"Aplicaciones en internet", 1, 1}},
		{{"Comprensión y redacción de textos", 1, 1}, {"Inglés para la comunicación oral", 1, 1}},
		{{"Solución de problemas", 1, 1}, {"Ética y deontología", 2, 0}},
		{{"Oportunidades de negocio", 1, 1}, {"Inglés técnico", 1, 1}},
		{{"Plan de negocios", 1, 1}, {"Innovación tecnológica", 1, 1}},
		{{"Trabajo colaborativo", 1, 1}, {"Investigación aplicada", 1, 1}},
	}
	efsrt := map[int]unitRow{1: {"Experiencias formativas en situaciones reales de trabajo I", 0, 4}, 3: {"Experiencias formativas en situaciones reales de trabajo II", 0, 4}, 5: {"Experiencias formativas en situaciones reales de trabajo III", 0, 4}}
	t := curriculumTemplate{
		name:    "Plan de estudios 2026",
		notes:   "Profesional técnico: 120 créditos en seis semestres. 1 crédito = 16 horas de teoría o 32 de práctica. Las EFSRT cierran cada módulo.",
		modules: modules[:],
	}
	for i, units := range semesters {
		semester := lowerASCII(roman(i + 1))
		module := modules[i/2].code
		add := func(component, kind string, u unitRow) {
			code := codeFrom("", u.name)
			if kind == domain.SubjectEFSRT {
				code = "efsrt-" + semester
			}
			t.subjects = append(t.subjects, subjectTemplate{
				kind: kind, component: component, code: code, name: u.name,
				hours: u.theory + 2*u.practice, theory: u.theory, practice: u.practice, grades: []string{semester}, module: module,
			})
		}
		for _, u := range units {
			add(domain.ComponentSpecific, domain.SubjectUnit, u)
		}
		for _, u := range employability[i] {
			add(domain.ComponentEmployability, domain.SubjectUnit, u)
		}
		if u, ok := efsrt[i]; ok {
			add(domain.ComponentEFSRT, domain.SubjectEFSRT, u)
		}
	}
	return t
}

var institutePrograms = map[string]curriculumTemplate{
	"enfermeria-tecnica": instituteProgram(
		[3]moduleTemplate{
			{"asistencia-basica", "Asistencia en servicios de salud y atención básica", "Semestres I y II."},
			{"salud-comunitaria", "Atención integral de la persona, la familia y la comunidad", "Semestres III y IV."},
			{"atencion-especializada", "Atención en el ciclo de vida y en emergencias", "Semestres V y VI."},
		},
		[6][]unitRow{
			{{"Fundamentos de enfermería", 2, 3}, {"Anatomía y fisiología humana", 2, 2}, {"Bioseguridad y control de infecciones", 1, 2}, {"Primeros auxilios", 2, 2}},
			{{"Técnicas de atención básica al paciente", 2, 3}, {"Farmacología básica", 2, 1}, {"Nutrición y dietética", 2, 1}},
			{{"Enfermería en salud comunitaria", 2, 3}, {"Salud materno infantil", 2, 3}, {"Atención en salud mental", 2, 1}, {"Promoción de la salud", 1, 2}},
			{{"Inmunizaciones y cadena de frío", 1, 2}, {"Atención del adulto y adulto mayor", 2, 3}, {"Administración de medicamentos", 1, 3}},
			{{"Atención en emergencias y desastres", 2, 3}, {"Enfermería quirúrgica básica", 2, 3}, {"Atención del paciente pediátrico", 2, 2}, {"Gestión de la información en salud", 1, 2}},
			{{"Cuidados paliativos y del paciente crónico", 2, 2}, {"Centro quirúrgico y esterilización", 1, 3}, {"Gestión de servicios de salud", 2, 2}},
		},
	),
	"desarrollo-sistemas": instituteProgram(
		[3]moduleTemplate{
			{"soporte-ti", "Soporte de tecnologías de la información", "Semestres I y II."},
			{"software-bd", "Desarrollo de software y bases de datos", "Semestres III y IV."},
			{"aplicaciones-web-moviles", "Desarrollo de aplicaciones web y móviles", "Semestres V y VI."},
		},
		[6][]unitRow{
			{{"Fundamentos de programación", 2, 3}, {"Arquitectura de computadoras", 2, 2}, {"Sistemas operativos", 1, 2}, {"Redes y comunicaciones", 2, 2}},
			{{"Programación orientada a objetos", 2, 3}, {"Base de datos", 2, 2}, {"Mantenimiento de equipos de cómputo", 1, 2}},
			{{"Análisis y diseño de sistemas", 2, 2}, {"Desarrollo de aplicaciones de escritorio", 2, 3}, {"Administración de base de datos", 2, 2}, {"Diseño de interfaces", 1, 2}},
			{{"Ingeniería de software", 2, 2}, {"Programación de servicios web", 2, 3}, {"Seguridad informática", 1, 2}},
			{{"Desarrollo de aplicaciones web", 2, 3}, {"Desarrollo de aplicaciones móviles", 2, 3}, {"Gestión de proyectos de software", 2, 2}, {"Pruebas de software", 1, 1}},
			{{"Integración de sistemas", 2, 2}, {"Computación en la nube", 1, 3}, {"Inteligencia de negocios", 2, 2}},
		},
	),
	"contabilidad": instituteProgram(
		[3]moduleTemplate{
			{"operaciones-contables", "Registro de operaciones contables", "Semestres I y II."},
			{"gestion-tributaria-laboral", "Gestión tributaria y laboral", "Semestres III y IV."},
			{"analisis-financiero", "Análisis financiero y auditoría", "Semestres V y VI."},
		},
		[6][]unitRow{
			{{"Contabilidad general", 2, 3}, {"Documentación comercial y contable", 2, 2}, {"Matemática financiera", 2, 1}, {"Legislación comercial", 2, 2}},
			{{"Contabilidad de sociedades", 2, 3}, {"Software contable", 1, 3}, {"Estadística aplicada", 2, 1}},
			{{"Tributación", 2, 3}, {"Contabilidad de costos", 2, 2}, {"Legislación laboral", 2, 2}, {"Planillas y remuneraciones", 1, 2}},
			{{"Contabilidad gubernamental", 2, 2}, {"Régimen tributario de empresas", 2, 3}, {"Finanzas empresariales", 1, 2}},
			{{"Análisis de estados financieros", 2, 3}, {"Presupuesto empresarial", 2, 2}, {"Auditoría financiera", 2, 2}, {"Contabilidad de entidades financieras", 1, 2}},
			{{"Auditoría tributaria", 2, 2}, {"Gestión financiera", 2, 2}, {"Proyectos de inversión", 2, 2}},
		},
	),
}

// curriculumTemplateFor returns the suggested plan of a level, if there is one
func curriculumTemplateFor(kind, levelCode string) (curriculumTemplate, bool) {
	switch kind {
	case domain.KindEBR:
		switch levelCode {
		case "inicial":
			return ebrInicial, true
		case "primaria":
			return ebrPrimaria, true
		case "secundaria":
			return ebrSecundaria, true
		}
	case domain.KindEBE:
		switch levelCode {
		case "prite":
			return ebePrite, true
		case "inicial":
			t := ebrInicial
			t.name, t.notes = "Plan CNEB adaptado", ebeNotes
			return t, true
		case "primaria":
			t := ebrPrimaria
			t.name, t.notes = "Plan CNEB adaptado", ebeNotes
			t.subjects = withoutHours(t.subjects)
			return t, true
		}
	case domain.KindEBA:
		switch levelCode {
		case "inicial":
			return ebaInicial, true
		case "intermedio":
			return ebaIntermedio, true
		case "avanzado":
			return ebaAvanzado, true
		}
	case domain.KindCEPRE:
		switch {
		case levelCode == "uni":
			return cepreUNI(), true
		case len(levelCode) == 7 && levelCode[:6] == "unmsm-":
			return cepreSanMarcos(levelCode[6:]), true
		}
	case domain.KindInstitute:
		t, ok := institutePrograms[levelCode]
		return t, ok
	}
	return curriculumTemplate{}, false
}

func withoutHours(subjects []subjectTemplate) []subjectTemplate {
	out := make([]subjectTemplate, len(subjects))
	for i, s := range subjects {
		s.hours = 0
		out[i] = s
	}
	return out
}
