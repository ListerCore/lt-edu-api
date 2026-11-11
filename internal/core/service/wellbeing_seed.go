package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// WellbeingSeeder is what the demo needs besides the repository (cmd/seed only)
type WellbeingSeeder interface {
	HasWellbeing(ctx context.Context, institutionID string) (bool, error)
	SeedTutors(ctx context.Context, institutionID string) (map[string]string, error)
	SeedStaffNamed(ctx context.Context, institutionID, prefix string) (string, error)
	SeedStaff(ctx context.Context, institutionID, code, dni, given, surnames, email, position string) (string, error)
	SeedConduct(ctx context.Context, institutionID, studentID string, score int, note string) error
}

type seedTopic struct {
	kind, topic, notes, agreements string
	commitments                    []struct{ party, text string }
}

func commit(pairs ...string) []struct{ party, text string } {
	var out []struct{ party, text string }
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, struct{ party, text string }{pairs[i], pairs[i+1]})
	}
	return out
}

var seedTopics = []seedTopic{
	{"family", "Bajo rendimiento en Matemática", "La familia cuenta que el estudiante dedica poco tiempo a las tareas en casa y que se distrae con el celular. Se revisan las evidencias del bimestre.",
		"Horario de estudio de una hora diaria sin celular. Revisión semanal de la agenda.", commit("family", "Supervisar el horario de estudio diario y firmar la agenda cada viernes.", "student", "Entregar las tareas de Matemática completas durante el mes.")},
	{"student", "Tardanzas reiteradas", "El estudiante llega tarde por quedarse dormido; refiere que se acuesta tarde jugando en línea.",
		"Acostarse antes de las 10:30 p. m. y llegar a la hora.", commit("student", "Llegar puntual durante las próximas cuatro semanas.")},
	{"family", "Inasistencias sin justificar", "Faltas seguidas en el mes. La madre explica un problema de salud de un familiar que obligó al estudiante a cuidar a sus hermanos.",
		"La familia avisará el mismo día cuando el estudiante no pueda asistir. Se coordinará la recuperación de actividades.", commit("family", "Comunicar y justificar cualquier inasistencia el mismo día.", "school", "Enviar las actividades pendientes y fijar fechas de recuperación.")},
	{"student", "Conflicto con compañeros", "Discusión en el recreo con un compañero del aula. Ambos reconocen el problema y aceptan conversar con mediación del tutor.",
		"Disculpa mutua y reubicación de asientos por un mes.", commit("student", "Resolver los desacuerdos conversando y, si no se puede, acudir al tutor.")},
	{"student", "Desmotivación y bajo ánimo", "Se le nota callado y aislado en las últimas semanas. Cuenta que se siente presionado por las notas. Se le ofrece apoyo de psicología.",
		"Conversación quincenal con el tutor. Se informa a la familia con su consentimiento.", commit("school", "Acompañamiento quincenal del tutor y coordinación con psicología.")},
	{"family", "Entrega del informe de progreso", "Se revisan los logros del bimestre, las competencias en proceso y las recomendaciones de los docentes.",
		"La familia apoyará la lectura diaria en casa.", commit("family", "Leer con el estudiante veinte minutos al día y registrar los libros leídos.")},
	{"family", "Uso del celular en clase", "Reiterado uso del celular durante las clases pese a las indicaciones. La familia desconocía la situación.",
		"El celular se quedará en casa o se entregará en la dirección al ingresar.", commit("family", "Revisar que el estudiante no lleve el celular al colegio.", "student", "Respetar la norma del celular en el aula.")},
	{"student", "Orientación vocacional", "El estudiante consulta sobre carreras técnicas y universitarias. Muestra interés por la ingeniería y el diseño.",
		"Participará en la feria vocacional y en una entrevista con psicología.", commit("student", "Investigar tres carreras de interés y conversar sobre ellas en la próxima tutoría.")},
	{"family", "Seguimiento de compromisos", "Se revisa el cumplimiento de los compromisos del mes anterior: hay avances en puntualidad y tareas.",
		"Se mantienen los acuerdos y se felicita al estudiante por el progreso.", nil},
}

var psychReasons = []string{
	"Se muestra retraída, llora con frecuencia y ha bajado su participación en clase.",
	"Episodios de irritabilidad y conflictos con compañeros en las últimas semanas.",
	"La familia comunica una separación reciente de los padres y cambios de conducta en casa.",
	"Dificultades de atención y concentración que afectan su aprendizaje.",
	"Refiere ansiedad antes de las evaluaciones y problemas para dormir.",
	"Posible situación de acoso escolar: recibe burlas de compañeros en redes sociales.",
	"Duelo por el fallecimiento de un familiar cercano.",
	"Consumo excesivo de videojuegos y aislamiento social.",
}

var psychNotes = []string{
	"Primera entrevista. Se establece vínculo y se explica la confidencialidad. Se exploran los motivos de la derivación.",
	"Se aplican instrumentos de tamizaje. Los resultados orientan a trabajar regulación emocional.",
	"Sesión con la familia: se acuerdan pautas de comunicación y rutinas en casa.",
	"Se trabajan técnicas de respiración y reconocimiento de emociones. Buena disposición.",
	"Coordinación con el tutor para ajustar la ubicación en el aula y reforzar logros.",
	"Se observa mejoría en el ánimo y en la relación con compañeros. Se espacian las sesiones.",
}

var sharedNotes = []string{
	"Reforzar sus logros en público y evitar comparaciones con compañeros.",
	"Darle un rol de responsabilidad en el aula; responde bien a la confianza.",
	"Avisar a psicología si se repiten episodios de llanto o aislamiento.",
	"",
}

type seedFault struct {
	category, text string
}

var minorFaults = []seedFault{
	{"rules", "No respetó los turnos de participación y se levantó reiteradamente de su lugar."},
	{"late", "Llegó 25 minutos tarde sin justificación."},
	{"presentation", "Asistió sin el uniforme de Educación Física pese al recordatorio."},
	{"disruption", "Interrumpió la clase con bromas y ruidos en varias ocasiones."},
	{"rules", "Comió durante la clase y dejó residuos en la carpeta."},
}

var seriousFaults = []seedFault{
	{"devices", "Usó el celular para grabar a un docente sin su consentimiento durante la clase."},
	{"aggression", "Empujó a un compañero durante el recreo; no hubo lesiones. Ambos fueron atendidos."},
	{"repeat_minor", "Acumula faltas leves en el bimestre pese a las medidas correctivas."},
	{"late_term", "Cuarta tardanza injustificada en el bimestre."},
}

var veryseriousFaults = []seedFault{
	{"bullying", "Burlas reiteradas y difusión de imágenes ofensivas de una compañera en un grupo de mensajería. Se activó el protocolo y se registró en SíseVe."},
	{"academic_fraud", "Suplantó a un compañero en una evaluación escrita."},
}

var correctives = []string{
	"Diálogo reflexivo con el estudiante y compromiso de mejora por escrito.",
	"Reparación: ordenar y limpiar el aula al final de la jornada durante una semana.",
	"Trabajo reflexivo sobre la norma incumplida, presentado al tutor.",
}

type seedSpecialist struct {
	code, dni, given, surnames, email, position, kind string
}

var ebeSpecialists = []seedSpecialist{
	{"P-0151", "32009151", "Carmen", "Huamaní Rojas", "carmen.huamani51@personal.test", "Terapeuta de lenguaje", "language"},
	{"P-0152", "32009152", "Óscar", "Vilca Paredes", "oscar.vilca52@personal.test", "Terapeuta físico", "physical"},
	{"P-0153", "32009153", "Rosa", "Quispe Llanos", "rosa.quispe53@personal.test", "Terapeuta ocupacional", "occupational"},
	{"P-0154", "32009154", "Liliana", "Torres Cárdenas", "liliana.torres54@personal.test", "Especialista SAANEE", "learning"},
}

var poiNeeds = []string{
	"Discapacidad intelectual moderada. Requiere apoyos en comunicación, autonomía y habilidades de la vida diaria.",
	"Trastorno del espectro autista. Dificultades en la interacción social y sensibilidad a los ruidos fuertes.",
	"Discapacidad física (parálisis cerebral espástica). Usa silla de ruedas y requiere apoyos para la escritura.",
	"Síndrome de Down. Avanza en lectura global; necesita apoyos visuales y tiempos adicionales.",
	"Discapacidad auditiva severa. Se comunica con lengua de señas peruana y lectura labial.",
	"Multidiscapacidad (intelectual y motora). Requiere apoyos generalizados y trabajo con la familia.",
}

var poiStrengths = []string{
	"Muestra interés por la música y responde bien a las rutinas establecidas.",
	"Es sociable con los adultos y le gusta ayudar en las tareas del aula.",
	"Buena memoria visual; reconoce imágenes y pictogramas con facilidad.",
	"Persistente en las tareas motoras finas cuando cuenta con el apoyo adecuado.",
}

var therapyGoals = map[string][]string{
	"language":     {"Ampliar el vocabulario funcional con pictogramas.", "Mejorar la articulación de fonemas /r/ y /s/.", "Usar frases de tres palabras para pedir objetos."},
	"physical":     {"Fortalecer el control de tronco en sedestación.", "Mejorar el equilibrio en bipedestación con apoyo.", "Mantener los rangos articulares de miembros inferiores."},
	"occupational": {"Mejorar la pinza fina para el agarre del lápiz.", "Ganar autonomía al ponerse y quitarse la casaca.", "Tolerar texturas nuevas en actividades sensoriales."},
	"learning":     {"Reconocer y escribir su nombre.", "Contar objetos hasta 10 con correspondencia uno a uno.", "Seguir una secuencia de tres pasos con apoyo visual."},
}

var homeTasks = []string{
	"Repetir en casa el juego de pictogramas durante 10 minutos al día.",
	"Practicar los ejercicios de estiramiento indicados antes de dormir.",
	"Dejar que se vista solo por las mañanas, con supervisión.",
	"Leer juntos un cuento corto señalando las imágenes.",
	"",
}

// SeedDemoWellbeing gives the demo institutions tutoring interviews and commitments, psychology cases, school
// discipline (faults, measures, a reconsideration) and, in special education, therapists, POIs and therapy
// sessions. cmd/seed runs it once
func SeedDemoWellbeing(ctx context.Context, svc *WellbeingService, seeder WellbeingSeeder, slugs []string) (int, error) {
	total := 0
	for n, slug := range slugs {
		inst, err := svc.attendance.academic.repo.InstitutionBySlug(ctx, slug)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return total, err
		}
		done, err := seeder.HasWellbeing(ctx, inst.ID)
		if err != nil {
			return total, err
		}
		if done {
			continue
		}
		count, err := seedWellbeing(ctx, svc, seeder, inst, rand.New(rand.NewPCG(uint64(n+70), 11)))
		total += count
		if err != nil {
			return total, fmt.Errorf("%s: %w", slug, err)
		}
	}
	return total, nil
}

func seedWellbeing(ctx context.Context, svc *WellbeingService, seeder WellbeingSeeder, inst domain.Institution, rnd *rand.Rand) (int, error) {
	repo := svc.repo
	today := svc.today()
	students, err := repo.Students(ctx, port.WellStudentFilter{InstitutionID: inst.ID})
	if err != nil || len(students) == 0 {
		return 0, err
	}
	periodID, _, err := repo.CurrentPeriod(ctx, inst.ID, today)
	if err != nil || periodID == "" {
		return 0, err
	}
	tutors, err := seeder.SeedTutors(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	psychologist, err := seeder.SeedStaffNamed(ctx, inst.ID, "Psic")
	if err != nil {
		return 0, err
	}
	director, err := seeder.SeedStaffNamed(ctx, inst.ID, "Director")
	if err != nil {
		return 0, err
	}
	count := 0
	day := func(daysAgo int) time.Time {
		d := today.AddDate(0, 0, -daysAgo)
		for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			d = d.AddDate(0, 0, -1)
		}
		return d
	}
	pick := func(list []string) string { return list[rnd.IntN(len(list))] }

	// Tutoring: one in six students had an interview, some two; most commitments past due were reviewed
	for _, st := range students {
		if rnd.IntN(6) != 0 {
			continue
		}
		tutor := tutors[st.SectionID]
		if tutor == "" {
			tutor = "Tutoría"
		}
		for k := 0; k < 1+rnd.IntN(2); k++ {
			t := seedTopics[rnd.IntN(len(seedTopics))]
			held := day(5 + rnd.IntN(200))
			iv, err := repo.SaveInterview(ctx, inst.ID, domain.Interview{StudentID: st.ID, Kind: t.kind, HeldOn: held, Topic: t.topic, Notes: t.notes, Agreements: t.agreements, AuthorName: tutor})
			if err != nil {
				return count, err
			}
			count++
			for _, c := range t.commitments {
				due := held.AddDate(0, 0, 14+rnd.IntN(21))
				saved, err := repo.SaveCommitment(ctx, inst.ID, domain.Commitment{StudentID: st.ID, InterviewID: iv.ID, Party: c.party, Description: c.text, DueOn: due, AuthorName: tutor})
				if err != nil {
					return count, err
				}
				if due.Before(today) {
					status := "met"
					switch r := rnd.IntN(20); {
					case r < 3:
						status = "unmet"
					case r < 6:
						status = "pending"
					}
					if status != "pending" {
						on := due.AddDate(0, 0, rnd.IntN(4))
						if on.After(today) {
							on = today
						}
						if err := repo.CheckCommitment(ctx, inst.ID, saved.ID, status, &on); err != nil {
							return count, err
						}
					}
				}
			}
		}
	}

	// Psychology: about 3 % of students; a few referrals still waiting, one urgent
	if psychologist != "" {
		origins := []string{"tutor", "tutor", "family", "teacher", "self", "direction"}
		urgentDone := false
		for _, st := range students {
			if rnd.IntN(33) != 0 {
				continue
			}
			opened := day(3 + rnd.IntN(180))
			origin := origins[rnd.IntN(len(origins))]
			referred := tutors[st.SectionID]
			switch origin {
			case "family":
				referred = "Familia del estudiante"
			case "self":
				referred = st.Name
			case "direction":
				referred = director
			case "teacher":
				referred = "Docente del aula"
			}
			c := domain.PsychCase{StudentID: st.ID, Origin: origin, ReferredName: referred, Reason: pick(psychReasons), Priority: "normal", Status: "referred", OpenedOn: opened}
			if !urgentDone && opened.After(today.AddDate(0, 0, -15)) {
				c.Priority, urgentDone = "urgent", true
			}
			saved, err := repo.OpenCase(ctx, inst.ID, c)
			if err != nil {
				return count, err
			}
			count++
			if opened.After(today.AddDate(0, 0, -10)) && rnd.IntN(2) == 0 {
				continue // still waiting for the first contact
			}
			sessions := 1 + rnd.IntN(5)
			kinds := []string{"individual", "individual", "evaluation", "family", "coordination", "individual"}
			at := opened
			for k := 0; k < sessions; k++ {
				at = at.AddDate(0, 0, 4+rnd.IntN(10))
				if at.After(today) {
					break
				}
				if _, err := repo.AddSession(ctx, domain.PsychSession{CaseID: saved.ID, HeldOn: at, Kind: kinds[k%len(kinds)], Notes: psychNotes[k%len(psychNotes)],
					NextSteps: "Continuar con el plan acordado.", AuthorName: psychologist}); err != nil {
					return count, err
				}
			}
			saved.Status, saved.PsychologistName, saved.SharedNote = "open", psychologist, pick(sharedNotes)
			switch r := rnd.IntN(10); {
			case r < 3:
				saved.Status = "follow_up"
			case r < 6 && at.Before(today.AddDate(0, 0, -20)):
				saved.Status = "closed"
				closed := at.AddDate(0, 0, 7)
				saved.ClosedOn = &closed
			}
			if err := repo.UpdateCase(ctx, inst.ID, saved); err != nil {
				return count, err
			}
		}
	}

	// Discipline (basic education): minor faults with corrective measures, some serious ones with suspensions, and
	// one student on conditional enrollment with a reconsideration still waiting for dirección
	if inst.Kind == domain.KindEBR || inst.Kind == domain.KindEBA || inst.Kind == domain.KindEBE {
		n, err := seedDiscipline(ctx, svc, seeder, inst, students, tutors, director, today, day, rnd)
		count += n
		if err != nil {
			return count, err
		}
	}

	// Special education: therapists, a POI for most students with special needs and their sessions
	if inst.Kind == domain.KindEBE {
		n, err := seedSpecialEd(ctx, svc, seeder, inst, students, periodID, director, today, rnd)
		count += n
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

func seedDiscipline(ctx context.Context, svc *WellbeingService, seeder WellbeingSeeder, inst domain.Institution, students []domain.WellStudent, tutors map[string]string,
	director string, today time.Time, day func(int) time.Time, rnd *rand.Rand) (int, error) {
	repo := svc.repo
	count := 0
	report := func(st domain.WellStudent, f seedFault, on time.Time, by string, notified bool, status string) (domain.Incident, error) {
		c, _ := category(f.category)
		x := domain.Incident{StudentID: st.ID, OccurredOn: on, Severity: c.Severity, Category: f.category, Description: f.text, ReportedName: by, Status: status}
		if notified {
			n := on.AddDate(0, 0, 1)
			if n.After(today) {
				n = today
			}
			x.FamilyNotifiedOn = &n
		}
		count++
		return repo.SaveIncident(ctx, inst.ID, x)
	}
	measure := func(m domain.Measure) (domain.Measure, error) {
		m.DecidedName = director
		return repo.AddMeasure(ctx, inst.ID, m)
	}
	// Minor faults: one in twelve students
	for _, st := range students {
		if rnd.IntN(12) != 0 {
			continue
		}
		by := tutors[st.SectionID]
		for k := 0; k < 1+rnd.IntN(2); k++ {
			f := minorFaults[rnd.IntN(len(minorFaults))]
			x, err := report(st, f, day(3+rnd.IntN(200)), by, rnd.IntN(2) == 0, "closed")
			if err != nil {
				return count, err
			}
			if _, err := repo.AddMeasure(ctx, inst.ID, domain.Measure{StudentID: st.ID, IncidentID: x.ID, Kind: "corrective", Detail: correctives[rnd.IntN(len(correctives))],
				StartsOn: x.OccurredOn, DecidedName: by}); err != nil {
				return count, err
			}
		}
	}
	// Serious faults: one in fifty students; half got a suspension
	for _, st := range students {
		if rnd.IntN(50) != 0 {
			continue
		}
		on := day(10 + rnd.IntN(150))
		x, err := report(st, seriousFaults[rnd.IntN(len(seriousFaults))], on, tutors[st.SectionID], true, "closed")
		if err != nil {
			return count, err
		}
		if rnd.IntN(2) == 0 {
			n := on.AddDate(0, 0, 1)
			if _, err := measure(domain.Measure{StudentID: st.ID, IncidentID: x.ID, Kind: "suspension", Ground: "serious_fault", Days: 1 + rnd.IntN(2), StartsOn: addSchoolDays(on, 1), NotifiedOn: &n,
				Detail: "Suspensión con actividades de reflexión enviadas a casa y reunión con la familia al retorno."}); err != nil {
				return count, err
			}
		} else if _, err := measure(domain.Measure{StudentID: st.ID, IncidentID: x.ID, Kind: "corrective", Detail: "Compromiso firmado por el estudiante y la familia, con seguimiento del tutor."}); err != nil {
			return count, err
		}
	}
	// The demo's hard case (EBR only): two suspensions this year, failing conduct, conditional enrollment notified
	// two school days ago and a reconsideration the family filed yesterday
	if inst.Kind == domain.KindEBR && len(students) > 40 {
		st := students[len(students)/3]
		by := tutors[st.SectionID]
		first, err := report(st, seriousFaults[1], day(120), by, true, "closed")
		if err != nil {
			return count, err
		}
		n1 := first.OccurredOn.AddDate(0, 0, 1)
		if _, err := measure(domain.Measure{StudentID: st.ID, IncidentID: first.ID, Kind: "suspension", Ground: "serious_fault", Days: 2, StartsOn: addSchoolDays(first.OccurredOn, 1), NotifiedOn: &n1,
			Detail: "Suspensión de dos días con plan de actividades y entrevista con psicología al retorno."}); err != nil {
			return count, err
		}
		second, err := report(st, seriousFaults[0], day(40), by, true, "closed")
		if err != nil {
			return count, err
		}
		n2 := second.OccurredOn.AddDate(0, 0, 1)
		if _, err := measure(domain.Measure{StudentID: st.ID, IncidentID: second.ID, Kind: "suspension", Ground: "serious_fault", Days: 3, StartsOn: addSchoolDays(second.OccurredOn, 1), NotifiedOn: &n2,
			Detail: "Suspensión de tres días. Se deriva a psicología y se cita a la familia."}); err != nil {
			return count, err
		}
		if err := seeder.SeedConduct(ctx, inst.ID, st.ID, 9, "Reiteradas faltas a las normas de convivencia y dos suspensiones en el año."); err != nil {
			return count, err
		}
		decided := day(4)
		notified := addSchoolDays(decided, 1)
		cond, err := measure(domain.Measure{StudentID: st.ID, Kind: "conditional", Ground: "two_suspensions", StartsOn: decided, NotifiedOn: &notified,
			Detail: "Matrícula condicional para el próximo año por acumular dos suspensiones. Plan de acompañamiento con tutoría y psicología; se evaluará en cada bimestre."})
		if err != nil {
			return count, err
		}
		if err := repo.RequestReconsideration(ctx, inst.ID, cond.ID, addSchoolDays(notified, 1), "La familia presenta el informe de psicología externa y se compromete a acompañar al estudiante en el plan.", "Familia del estudiante"); err != nil {
			return count, err
		}
		// A second student: very serious fault this week, still open (it shows in the alerts)
		other := students[len(students)/2]
		if _, err := report(other, veryseriousFaults[0], day(2), tutors[other.SectionID], true, "open"); err != nil {
			return count, err
		}
		if err := seeder.SeedConduct(ctx, inst.ID, other.ID, 10, "Conducta que afecta la convivencia del aula."); err != nil {
			return count, err
		}
	}
	return count, nil
}

func seedSpecialEd(ctx context.Context, svc *WellbeingService, seeder WellbeingSeeder, inst domain.Institution, students []domain.WellStudent, periodID, director string,
	today time.Time, rnd *rand.Rand) (int, error) {
	repo := svc.repo
	count := 0
	ids := make([]string, len(ebeSpecialists))
	for i, sp := range ebeSpecialists {
		id, err := seeder.SeedStaff(ctx, inst.ID, sp.code, sp.dni, sp.given, sp.surnames, sp.email, sp.position)
		if err != nil {
			return count, err
		}
		ids[i] = id
	}
	start := time.Date(today.Year(), 3, 16, 0, 0, 0, 0, time.UTC)
	k := 0
	for _, st := range students {
		if !st.SpecialNeeds {
			continue
		}
		k++
		// One in eight still has no POI (the alerts list them)
		if k%8 == 0 {
			continue
		}
		sp := rnd.IntN(len(ebeSpecialists))
		needs := poiNeeds[rnd.IntN(len(poiNeeds))]
		p, err := repo.SavePlan(ctx, inst.ID, domain.IndividualPlan{StudentID: st.ID, PeriodID: periodID, SpecialistID: ids[sp], Needs: needs, Strengths: poiStrengths[rnd.IntN(len(poiStrengths))],
			EducationalResponse:   "Enseñanza estructurada con rutinas visuales, trabajo en pequeños grupos y apoyo individual en las áreas de Comunicación y Matemática. Participa en todas las actividades del aula con los apoyos señalados.",
			CurricularAdaptations: "Se priorizan las competencias de comunicación oral, lectura de imágenes y resolución de problemas de cantidad. Se ajustan los desempeños al nivel de inicio del grado anterior.",
			AccessAdaptations:     "Ubicación cerca de la pizarra y lejos de ruidos. Uso de pictogramas, agenda visual y tiempos adicionales. Materiales con letra ampliada.",
			EvaluationCriteria:    "Se evalúa con la escala AD/A/B/C frente a los desempeños adaptados del plan, con observación directa y portafolio de evidencias.",
			Actors:                "Docente de aula, " + ebeSpecialists[sp].position + " (" + ebeSpecialists[sp].given + " " + ebeSpecialists[sp].surnames + "), psicología y familia.",
			FamilyCommitments:     "Asistir a las reuniones bimestrales, practicar en casa las tareas de las terapias y mantener las rutinas de sueño y alimentación."})
		if err != nil {
			return count, err
		}
		count++
		if rnd.IntN(10) < 7 {
			approved := start.AddDate(0, 0, 10+rnd.IntN(30))
			if err := repo.SetPlanStatus(ctx, inst.ID, p.ID, "approved", director, &approved); err != nil {
				return count, err
			}
		}
		// Every other week since March with the plan's specialist (some sessions missed)
		kind := ebeSpecialists[sp].kind
		goals := therapyGoals[kind]
		for d := start.AddDate(0, 0, rnd.IntN(10)); d.Before(today); d = d.AddDate(0, 0, 14) {
			if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
				d = addSchoolDays(d, 1)
			}
			attended := rnd.IntN(10) != 0
			notes := "Buena disposición; se observan avances respecto a la sesión anterior."
			if !attended {
				notes = "No asistió; la familia avisó por enfermedad."
			}
			if _, err := repo.SaveTherapy(ctx, inst.ID, domain.TherapySession{StudentID: st.ID, TherapistID: ids[sp], TherapistName: ebeSpecialists[sp].given + " " + ebeSpecialists[sp].surnames,
				Kind: kind, HeldOn: d, Minutes: 45, Attended: attended, Goal: goals[rnd.IntN(len(goals))], Notes: notes, HomeTasks: homeTasks[rnd.IntN(len(homeTasks))]}); err != nil {
				return count, err
			}
		}
	}
	return count, nil
}
