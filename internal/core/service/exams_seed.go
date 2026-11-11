package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"api/internal/core/domain"
)

// ExamsSeeder writes what the academy demo needs besides the service (cmd/seed only)
type ExamsSeeder interface {
	HasBank(ctx context.Context, institutionID string) (bool, error)
	// SeedAlumni creates former students (graduated) for the admissions list
	SeedAlumni(ctx context.Context, institutionID string, people [][3]string) ([]string, error)
}

// seedQuestion is a bank question before it gets its letters: the right option is the first one
type seedQuestion struct {
	course, topic, stem, explanation, difficulty string
	options                                      [5]string
}

func num(v int) string { return strconv.Itoa(v) }

// distinct numeric distractors around a value
func numericOptions(rnd *rand.Rand, right int, spread int) [5]string {
	opts := [5]string{num(right)}
	seen := map[int]bool{right: true}
	for i := 1; i < 5; {
		d := rnd.IntN(2*spread+1) - spread
		v := right + d
		if d == 0 || v <= 0 || seen[v] {
			continue
		}
		seen[v] = true
		opts[i] = num(v)
		i++
	}
	return opts
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// generated questions: computed from random data, so every key is right
func generatedQuestions(rnd *rand.Rand) []seedQuestion {
	var out []seedQuestion
	add := func(course, topic, diff, stem string, opts [5]string, why string) {
		out = append(out, seedQuestion{course: course, topic: topic, difficulty: diff, stem: stem, options: opts, explanation: why})
	}
	for range 3 {
		a, b := (rnd.IntN(8)+2)*6, (rnd.IntN(8)+2)*4
		l := a / gcd(a, b) * b
		add("aritmetica", "Divisibilidad y números primos", "easy", fmt.Sprintf("¿Cuál es el mínimo común múltiplo de %d y %d?", a, b), numericOptions(rnd, l, 24), fmt.Sprintf("MCD = %d, así que MCM = %d × %d ÷ %d = %d.", gcd(a, b), a, b, gcd(a, b), l))
		p, base := (rnd.IntN(8)+1)*5, (rnd.IntN(9)+2)*40
		add("aritmetica", "Tanto por ciento", "easy", fmt.Sprintf("¿Cuánto es el %d %% de %d?", p, base), numericOptions(rnd, p*base/100, 20), fmt.Sprintf("%d × %d ÷ 100 = %d.", base, p, p*base/100))
		w, d := rnd.IntN(4)+3, (rnd.IntN(5)+2)*6
		add("aritmetica", "Magnitudes proporcionales", "medium", fmt.Sprintf("%d obreros hacen una obra en %d días. ¿En cuántos días la harían %d obreros trabajando al mismo ritmo?", w, d, w*2), numericOptions(rnd, d/2, 6),
			"Obreros y días son inversamente proporcionales: el doble de obreros tarda la mitad.")

		x, k, c := rnd.IntN(12)+2, rnd.IntN(6)+2, rnd.IntN(20)+1
		add("algebra", "Ecuaciones e inecuaciones", "easy", fmt.Sprintf("Resuelve: %dx + %d = %d", k, c, k*x+c), numericOptions(rnd, x, 5), fmt.Sprintf("%dx = %d, entonces x = %d.", k, k*x, x))
		m, n := rnd.IntN(6)+2, rnd.IntN(6)+2
		add("algebra", "Leyes de exponentes", "easy", fmt.Sprintf("Simplifica: 2^%d · 2^%d ÷ 2^%d. El resultado es 2 elevado a:", m, n, 2), numericOptions(rnd, m+n-2, 4), "En productos se suman los exponentes y en divisiones se restan.")
		e := rnd.IntN(8) + 3
		add("algebra", "Logaritmos", "medium", fmt.Sprintf("Calcula log₂ %d", 1<<e), numericOptions(rnd, e, 3), fmt.Sprintf("2^%d = %d.", e, 1<<e))
		ap, bp := rnd.IntN(7)+2, rnd.IntN(7)+2
		add("algebra", "Productos notables", "medium", fmt.Sprintf("Si a = %d y b = %d, ¿cuánto vale a² + 2ab + b²?", ap, bp), numericOptions(rnd, (ap+bp)*(ap+bp), 15), fmt.Sprintf("Es (a + b)² = %d² = %d.", ap+bp, (ap+bp)*(ap+bp)))

		α, β := (rnd.IntN(10)+3)*5, (rnd.IntN(10)+3)*5
		add("geometria", "Triángulos", "easy", fmt.Sprintf("Dos ángulos de un triángulo miden %d° y %d°. ¿Cuánto mide el tercero?", α, β), numericOptions(rnd, 180-α-β, 20), "Los ángulos interiores de un triángulo suman 180°.")
		bb, hh := (rnd.IntN(8)+2)*2, rnd.IntN(9)+3
		add("geometria", "Áreas de regiones planas", "easy", fmt.Sprintf("Un triángulo tiene base %d cm y altura %d cm. ¿Cuál es su área en cm²?", bb, hh), numericOptions(rnd, bb*hh/2, 12), "Área = base × altura ÷ 2.")
		sides := rnd.IntN(7) + 5
		add("geometria", "Polígonos y cuadriláteros", "medium", fmt.Sprintf("¿Cuánto suman, en grados, los ángulos interiores de un polígono de %d lados?", sides),
			[5]string{num(180 * (sides - 2)), num(180 * (sides - 1)), num(180 * sides), num(180 * (sides - 3)), num(360 * (sides - 2))}, "Suma = 180° × (n − 2).")

		v, t := (rnd.IntN(8)+2)*5, rnd.IntN(8)+2
		add("fisica", "Cinemática", "easy", fmt.Sprintf("Un móvil con MRU va a %d m/s. ¿Qué distancia recorre en %d s, en metros?", v, t), numericOptions(rnd, v*t, 30), "En el MRU, d = v · t.")
		mass, acc := rnd.IntN(9)+2, rnd.IntN(6)+2
		add("fisica", "Estática y dinámica", "easy", fmt.Sprintf("¿Qué fuerza, en newtons, acelera %d kg a %d m/s²?", mass, acc), numericOptions(rnd, mass*acc, 10), "Segunda ley de Newton: F = m · a.")
		f, dd := (rnd.IntN(8)+2)*10, rnd.IntN(9)+2
		add("fisica", "Trabajo y energía", "medium", fmt.Sprintf("Una fuerza de %d N desplaza un cuerpo %d m en su misma dirección. ¿Qué trabajo realiza, en joules?", f, dd), numericOptions(rnd, f*dd, 60), "W = F · d cuando la fuerza va en la dirección del movimiento.")
		i, r := rnd.IntN(5)+2, (rnd.IntN(8)+2)*5
		add("fisica", "Electricidad", "medium", fmt.Sprintf("Por una resistencia de %d Ω circula una corriente de %d A. ¿Cuál es el voltaje, en voltios?", r, i), numericOptions(rnd, i*r, 20), "Ley de Ohm: V = I · R.")

		a1, d1 := rnd.IntN(9)+1, rnd.IntN(6)+2
		add("razonamiento-matematico", "Sucesiones", "easy", fmt.Sprintf("¿Qué número sigue? %d, %d, %d, %d, …", a1, a1+d1, a1+2*d1, a1+3*d1), numericOptions(rnd, a1+4*d1, 6), fmt.Sprintf("Cada término suma %d.", d1))
		add("habilidad-logico-matematica", "", "easy", fmt.Sprintf("¿Qué número sigue? %d, %d, %d, %d, …", a1*2, a1*4, a1*8, a1*16), numericOptions(rnd, a1*32, 20), "Cada término es el doble del anterior.")
		son := rnd.IntN(8) + 6
		add("razonamiento-matematico", "Planteo de ecuaciones", "medium", fmt.Sprintf("Un padre tiene el triple de la edad de su hijo y entre los dos suman %d años. ¿Cuántos años tiene el hijo?", 4*son),
			numericOptions(rnd, son, 5), fmt.Sprintf("x + 3x = %d, entonces x = %d.", 4*son, son))
	}
	return out
}

// curated questions with fixed facts
var curatedQuestions = []seedQuestion{
	{"trigonometria", "Razones trigonométricas", "¿Cuánto vale sen 30°?", "", "easy", [5]string{"1/2", "√3/2", "√2/2", "1", "0"}},
	{"trigonometria", "Razones trigonométricas", "¿Cuánto vale tan 45°?", "", "easy", [5]string{"1", "0", "√3", "1/2", "√2"}},
	{"trigonometria", "Identidades trigonométricas", "Para todo ángulo x, sen²x + cos²x es igual a:", "", "easy", [5]string{"1", "0", "2", "tan x", "sec x"}},
	{"trigonometria", "Razones trigonométricas", "¿Cuánto vale cos 60°?", "", "easy", [5]string{"1/2", "√3/2", "0", "1", "√2/2"}},
	{"quimica", "Tabla periódica", "¿Cuál es el número atómico del sodio (Na)?", "", "easy", [5]string{"11", "12", "10", "19", "23"}},
	{"quimica", "Tabla periódica", "¿Qué elemento tiene número atómico 6?", "", "easy", [5]string{"Carbono", "Nitrógeno", "Oxígeno", "Boro", "Litio"}},
	{"quimica", "Estequiometría", "¿Cuál es la masa molar del agua (H₂O), en g/mol? (H = 1, O = 16)", "", "easy", [5]string{"18", "17", "16", "20", "34"}},
	{"quimica", "Estequiometría", "¿Cuál es la masa molar del CO₂, en g/mol? (C = 12, O = 16)", "", "medium", [5]string{"44", "28", "32", "40", "56"}},
	{"quimica", "Enlace químico", "El enlace entre el sodio y el cloro en la sal común es:", "", "easy", [5]string{"Iónico", "Covalente apolar", "Metálico", "Puente de hidrógeno", "Covalente coordinado"}},
	{"biologia", "La célula", "¿Qué organelo produce la mayor parte de la energía (ATP) de la célula?", "", "easy", [5]string{"Mitocondria", "Ribosoma", "Aparato de Golgi", "Lisosoma", "Vacuola"}},
	{"biologia", "Genética", "¿Qué base nitrogenada está en el ARN y no en el ADN?", "", "medium", [5]string{"Uracilo", "Timina", "Adenina", "Guanina", "Citosina"}},
	{"biologia", "Fisiología humana", "¿Qué órgano produce la insulina?", "", "easy", [5]string{"Páncreas", "Hígado", "Riñón", "Estómago", "Bazo"}},
	{"biologia", "Ecología", "En una cadena alimenticia, las plantas son:", "", "easy", [5]string{"Productores", "Consumidores primarios", "Consumidores secundarios", "Descomponedores", "Parásitos"}},
	{"historia-peru", "Independencia", "¿En qué año se proclamó la independencia del Perú en Lima?", "", "easy", [5]string{"1821", "1824", "1810", "1879", "1532"}},
	{"historia-peru", "Independencia", "La batalla de Ayacucho, que selló la independencia, fue en:", "", "easy", [5]string{"1824", "1821", "1820", "1830", "1814"}},
	{"historia-peru", "Perú prehispánico", "La ciudadela de Machu Picchu fue construida por los:", "", "easy", [5]string{"Incas", "Chavín", "Nazca", "Mochica", "Wari"}},
	{"historia-peru", "República", "La Guerra del Pacífico comenzó en:", "", "medium", [5]string{"1879", "1866", "1883", "1898", "1841"}},
	{"historia-universal", "", "La Revolución francesa comenzó en:", "", "easy", [5]string{"1789", "1776", "1815", "1848", "1492"}},
	{"historia-universal", "", "¿Qué hecho marca convencionalmente el inicio de la Edad Moderna?", "", "medium", [5]string{"La caída de Constantinopla (1453)", "La Revolución francesa", "La caída de Roma (476)", "La Primera Guerra Mundial", "La Reforma protestante"}},
	{"geografia", "", "¿Cuál es el río más largo del Perú que nace en los Andes y desemboca en el Atlántico?", "", "medium", [5]string{"Amazonas", "Rímac", "Santa", "Mantaro", "Urubamba"}},
	{"geografia", "", "¿En qué región natural del Perú se ubica Puno?", "", "easy", [5]string{"Sierra", "Costa", "Selva alta", "Selva baja", "Mar de Grau"}},
	{"economia", "Mercado", "Si sube el precio de un bien y lo demás no cambia, la cantidad demandada:", "", "easy", [5]string{"Disminuye", "Aumenta", "No cambia", "Se duplica", "Se vuelve cero"}},
	{"economia", "Dinero e inflación", "La inflación es el aumento sostenido de:", "", "easy", [5]string{"El nivel general de precios", "La producción", "El empleo", "Las exportaciones", "La tasa de natalidad"}},
	{"economia", "Comercio internacional", "Vender bienes nacionales a otros países se llama:", "", "easy", [5]string{"Exportación", "Importación", "Arancel", "Dumping", "Subsidio"}},
	{"educacion-civica", "", "¿Cada cuántos años se elige al presidente del Perú?", "", "easy", [5]string{"5", "4", "6", "3", "7"}},
	{"educacion-civica", "", "La norma de mayor jerarquía en el Perú es:", "", "easy", [5]string{"La Constitución", "La ley", "El decreto supremo", "La ordenanza", "La resolución ministerial"}},
	{"filosofia", "", "«Solo sé que nada sé» se atribuye a:", "", "easy", [5]string{"Sócrates", "Platón", "Aristóteles", "Descartes", "Kant"}},
	{"filosofia", "", "La rama de la filosofía que estudia el conocimiento es:", "", "medium", [5]string{"Epistemología", "Ética", "Estética", "Ontología", "Lógica"}},
	{"psicologia", "", "La memoria que guarda información por pocos segundos se llama:", "", "medium", [5]string{"Memoria a corto plazo", "Memoria a largo plazo", "Memoria semántica", "Memoria episódica", "Memoria procedimental"}},
	{"psicologia", "", "La etapa de la vida entre la niñez y la adultez es:", "", "easy", [5]string{"Adolescencia", "Infancia", "Senectud", "Lactancia", "Madurez"}},
	{"lenguaje", "Ortografía", "¿Qué palabra lleva tilde?", "", "easy", [5]string{"Árbol", "Cancion", "Mesa", "Reloj", "Pared"}},
	{"lenguaje", "Morfología", "En «Los niños juegan en el parque», el núcleo del sujeto es:", "", "easy", [5]string{"niños", "Los", "juegan", "parque", "en"}},
	{"lenguaje", "Fonética y fonología", "¿Cuántas sílabas tiene «murciélago»?", "", "easy", [5]string{"4", "3", "5", "6", "2"}},
	{"literatura", "Literatura peruana", "«Los ríos profundos» fue escrita por:", "", "easy", [5]string{"José María Arguedas", "Mario Vargas Llosa", "César Vallejo", "Ciro Alegría", "Ricardo Palma"}},
	{"literatura", "Literatura universal", "«Don Quijote de la Mancha» es obra de:", "", "easy", [5]string{"Miguel de Cervantes", "Lope de Vega", "Garcilaso de la Vega", "Quevedo", "Calderón de la Barca"}},
	{"literatura", "Literatura peruana", "«Los heraldos negros» es un poemario de:", "", "easy", [5]string{"César Vallejo", "José Santos Chocano", "Abraham Valdelomar", "Javier Heraud", "Blanca Varela"}},
	{"habilidad-verbal", "", "Elige el sinónimo de «efímero»:", "", "medium", [5]string{"Pasajero", "Eterno", "Sólido", "Amplio", "Feliz"}},
	{"habilidad-verbal", "", "Elige el antónimo de «prolijo»:", "", "medium", [5]string{"Descuidado", "Esmerado", "Detallado", "Minucioso", "Ordenado"}},
	{"habilidad-verbal", "", "Completa: El investigador ___ los datos antes de publicar sus conclusiones.", "", "easy", [5]string{"verificó", "olvidó", "perdió", "ignoró", "inventó"}},
	{"razonamiento-verbal", "Sinónimos y antónimos", "Elige el sinónimo de «perspicaz»:", "", "medium", [5]string{"Sagaz", "Torpe", "Lento", "Ingenuo", "Distraído"}},
	{"razonamiento-verbal", "Analogías", "MÉDICO : HOSPITAL ::", "", "easy", [5]string{"Profesor : Colegio", "Pan : Panadero", "Libro : Lector", "Árbol : Hoja", "Agua : Sed"}},
	{"razonamiento-verbal", "Oraciones incompletas", "Completa: La ___ del puente permitió unir ambos pueblos.", "", "easy", [5]string{"construcción", "destrucción", "ausencia", "caída", "venta"}},
	{"humanidades", "", "¿Quién escribió «Tradiciones peruanas»?", "", "easy", [5]string{"Ricardo Palma", "Manuel González Prada", "Abraham Valdelomar", "José Carlos Mariátegui", "Clorinda Matto de Turner"}},
	{"humanidades", "", "¿En qué año el Perú declaró la guerra al lado de los aliados en la Segunda Guerra Mundial?", "", "hard", [5]string{"1945", "1939", "1941", "1942", "1944"}},
}

// bankFor turns a seed question into a bank question with its options in a random order
func bankFor(rnd *rand.Rand, q seedQuestion) domain.Question {
	opts := q.options
	order := rnd.Perm(5)
	out := make([]string, 5)
	answer := ""
	for i, j := range order {
		out[i] = opts[j]
		if j == 0 {
			answer = letters[i]
		}
	}
	diff := q.difficulty
	if diff == "" {
		diff = "medium"
	}
	return domain.Question{CourseCode: q.course, Topic: q.topic, Stem: q.stem, Options: out, Answer: answer, Difficulty: diff, Explanation: q.explanation, AuthorName: "Banco de la academia"}
}

// SeedDemoExams fills the academy demo: the question bank, mock exams of the running cycle (published with their
// answer sheets, and one still being built) and former students who got into university. cmd/seed runs it once
func SeedDemoExams(ctx context.Context, svc *ExamsService, seeder ExamsSeeder, slug string) (int, error) {
	inst, err := svc.assessment.academic.repo.InstitutionBySlug(ctx, slug)
	if errors.Is(err, domain.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if done, err := seeder.HasBank(ctx, inst.ID); err != nil || done {
		return 0, err
	}
	rnd := rand.New(rand.NewPCG(88, 8))
	courses, err := svc.repo.Courses(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	known := map[string]bool{}
	for _, c := range courses {
		known[c.Code] = true
	}
	all := append(generatedQuestions(rnd), curatedQuestions...)
	for _, q := range all {
		if !known[q.course] {
			continue
		}
		if _, err := svc.repo.SaveQuestion(ctx, inst.ID, bankFor(rnd, q)); err != nil {
			return 0, fmt.Errorf("question %q: %w", q.stem, err)
		}
	}

	levels, err := svc.assessment.academic.repo.Levels(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	var sm, uni []string
	for _, l := range levels {
		if strings.HasPrefix(l.Code, "unmsm") {
			sm = append(sm, l.ID)
		} else if l.Code == "uni" {
			uni = append(uni, l.ID)
		}
	}
	periods, err := svc.assessment.academic.repo.Periods(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	i := slices.IndexFunc(periods, func(p domain.Period) bool {
		return p.Status == domain.PeriodActive && strings.Contains(p.Name, "Repaso")
	})
	if i < 0 {
		return 0, nil
	}
	p := periods[i]
	sheets := 0
	plan := []struct {
		name    string
		days    int
		levels  []string
		total   int
		publish bool
		effort  float64
	}{
		{"Simulacro San Marcos N.° 1", 5, sm, 100, true, 0},
		{"Simulacro UNI N.° 1", 6, uni, 60, true, 0},
		{"Simulacro San Marcos N.° 2", 8, sm, 100, true, 0.05},
		{"Simulacro San Marcos N.° 3", 12, sm, 100, false, 0},
	}
	students, err := svc.repo.PeriodStudents(ctx, inst.ID, p.ID)
	if err != nil {
		return 0, err
	}
	for _, x := range plan {
		if len(x.levels) == 0 {
			continue
		}
		m, err := svc.repo.SaveMockExam(ctx, inst.ID, domain.MockExam{PeriodID: p.ID, Name: x.name, HeldOn: p.StartsOn.AddDate(0, 0, x.days), Duration: 180, PointsRight: 20, PointsWrong: 1.125,
			Notes: "Modalidad de examen general de admisión."})
		if err != nil {
			return sheets, err
		}
		c, w := structureFromHours(courses, x.levels, x.total)
		if err := svc.repo.SetStructure(ctx, m.ID, c, w); err != nil {
			return sheets, err
		}
		key, err := svc.repo.Key(ctx, m.ID)
		if err != nil {
			return sheets, err
		}
		used := []string{}
		byCourse := map[string][]int{}
		for idx, k := range key {
			byCourse[k.CourseCode] = append(byCourse[k.CourseCode], idx)
		}
		for course, idx := range byCourse {
			qs, err := svc.repo.PickQuestions(ctx, inst.ID, course, len(idx), used)
			if err != nil {
				return sheets, err
			}
			for j := range idx {
				if j < len(qs) {
					key[idx[j]].QuestionID, key[idx[j]].Answer = qs[j].ID, qs[j].Answer
					used = append(used, qs[j].ID)
				} else {
					// More questions than the bank has: typed in the key only
					key[idx[j]].Answer = letters[rnd.IntN(5)]
				}
			}
		}
		if err := svc.repo.SaveKey(ctx, m.ID, key); err != nil {
			return sheets, err
		}
		if !x.publish {
			continue
		}
		m, err = svc.repo.MockExam(ctx, inst.ID, m.ID)
		if err != nil {
			return sheets, err
		}
		weights := svc.weightsFor(m)
		var batch []domain.AnswerSheet
		for _, st := range students {
			if !slices.Contains(x.levels, st.LevelID) || rnd.IntN(20) == 0 {
				continue
			}
			a := ability(st.ID)
			var b strings.Builder
			for _, k := range key {
				switch r := rnd.Float64(); {
				case r < 0.12+0.1*(1-a):
					b.WriteByte('-')
				case r < 0.12+0.1*(1-a)+0.2+0.55*a+x.effort:
					b.WriteString(k.Answer)
				default:
					wrong := letters[rnd.IntN(5)]
					for wrong == k.Answer {
						wrong = letters[rnd.IntN(5)]
					}
					b.WriteString(wrong)
				}
			}
			total, by := ScoreSheet(b.String(), key, weights[st.LevelID], m.PointsRight, m.PointsWrong)
			source := "reader"
			if rnd.IntN(10) == 0 {
				source = "manual"
			}
			batch = append(batch, domain.AnswerSheet{StudentID: st.ID, SectionID: st.SectionID, LevelID: st.LevelID, Answers: b.String(), Source: source, Score: total.Score,
				Right: total.Right, Wrong: total.Wrong, Blank: total.Blank, ByCourse: by})
		}
		if err := svc.repo.SaveSheets(ctx, m.ID, batch); err != nil {
			return sheets, err
		}
		sheets += len(batch)
		if err := svc.repo.SetStatus(ctx, inst.ID, m.ID, "published"); err != nil {
			return sheets, err
		}
	}

	// Former students and where they got in
	admitted := []struct {
		university, career, exam string
		place                    int
		score                    float64
		on                       string
	}{
		{"Universidad Nacional Mayor de San Marcos", "Medicina Humana", "Admisión 2026-I", 12, 1689.25, "2026-03-15"},
		{"Universidad Nacional Mayor de San Marcos", "Derecho", "Admisión 2026-I", 4, 1520.5, "2026-03-15"},
		{"Universidad Nacional Mayor de San Marcos", "Ingeniería de Sistemas", "Admisión 2026-I", 21, 1488.75, "2026-03-16"},
		{"Universidad Nacional de Ingeniería", "Ingeniería Civil", "Admisión 2026-I", 35, 14.82, "2026-02-20"},
		{"Universidad Nacional de Ingeniería", "Ingeniería Mecatrónica", "Admisión 2026-I", 8, 16.1, "2026-02-20"},
		{"Universidad Nacional Mayor de San Marcos", "Enfermería", "Admisión 2026-II", 17, 1402.0, "2026-09-13"},
		{"Universidad Nacional Mayor de San Marcos", "Economía", "Admisión 2026-II", 9, 1455.5, "2026-09-13"},
		{"Universidad Nacional de Ingeniería", "Ingeniería Industrial", "Admisión 2026-II", 41, 13.97, "2026-08-22"},
	}
	people := make([][3]string, len(admitted))
	for j := range admitted {
		given := adultFemale[rnd.IntN(len(adultFemale))]
		if j%2 == 1 {
			given = adultMale[rnd.IntN(len(adultMale))]
		}
		people[j] = [3]string{given, demoSurnames[rnd.IntN(len(demoSurnames))], demoSurnames[rnd.IntN(len(demoSurnames))]}
	}
	ids, err := seeder.SeedAlumni(ctx, inst.ID, people)
	if err != nil {
		return sheets, err
	}
	for j, a := range admitted {
		on, _ := time.Parse(time.DateOnly, a.on)
		place, score := a.place, a.score
		if _, err := svc.repo.SaveAdmission(ctx, inst.ID, domain.Admission{StudentID: ids[j], University: a.university, Career: a.career, Exam: a.exam, AdmittedOn: on, Place: &place, Score: &score}); err != nil {
			return sheets, err
		}
	}
	return sheets, nil
}
