package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Demo students: invented names, DNIs out of the real range and emails on the reserved .test domain
var (
	girlNames    = []string{"Valeria", "Camila", "Lucía", "Sofía", "Ariana", "Valentina", "Mariana", "Isabella", "Daniela", "Renata", "Ximena", "Fernanda", "Luciana", "Antonella", "Abigail", "Micaela", "Romina", "Kiara", "Nayeli", "Alessandra"}
	boyNames     = []string{"Mateo", "Diego", "Sebastián", "Thiago", "Gael", "Santiago", "Joaquín", "Adrián", "Leonardo", "Rodrigo", "Gabriel", "Fabricio", "Alonso", "Emiliano", "Bruno", "Jhon", "Piero", "Iker", "Dylan", "Franco"}
	adultFemale  = []string{"Rosa", "Carmen", "Elena", "Marta", "Gloria", "Julia", "Norma", "Silvia", "Patricia", "Yolanda", "Teresa", "Luz"}
	adultMale    = []string{"Julio", "Óscar", "Víctor", "Raúl", "Hugo", "César", "Walter", "Edwin", "Freddy", "Javier", "Manuel", "Percy"}
	demoSurnames = []string{"Ccori", "Huamán", "Quispe", "Mamani", "Paredes", "Rojas", "Flores", "Torres", "Condori", "Mendoza", "Chávez", "Ramos", "Apaza", "Vargas", "Huanca", "Cárdenas", "Ríos", "Gutiérrez", "Choque", "Salazar", "Ccahuana", "Pari", "Tito", "Yupanqui", "Villca", "Soto", "Medina", "Vilca", "Paucar", "Ayala"}
	occupations  = []string{"Comerciante", "Docente", "Enfermera", "Chofer", "Contador", "Ama de casa", "Agricultor", "Técnico electricista", "Costurera", "Ingeniero civil", "Vendedor", "Policía"}
)

func ascii(s string) string {
	r := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n", "Ó", "O", "Á", "A")
	return strings.ToLower(r.Replace(s))
}

// ageFor is the usual age of a grade on 31 March, to give demo students a plausible birth date
func ageFor(kind, levelCode string, gradePos int, rnd *rand.Rand) int {
	switch kind {
	case domain.KindEBR:
		switch levelCode {
		case "inicial":
			return 3 + gradePos
		case "primaria":
			return 6 + gradePos
		default:
			return 12 + gradePos
		}
	case domain.KindEBE:
		switch levelCode {
		case "prite":
			return gradePos
		case "inicial":
			return 3 + gradePos
		default:
			return 7 + gradePos + rnd.IntN(3)
		}
	case domain.KindEBA:
		return 16 + rnd.IntN(30)
	case domain.KindCEPRE:
		return 16 + rnd.IntN(4)
	default:
		return 17 + gradePos/2 + rnd.IntN(6)
	}
}

// SeedDemoPeople fills the sections of the running periods of the ListerCore Edu demo institutions with students, their
// guardians and their enrollments (and the 2025 history of the school). cmd/seed runs it once
func SeedDemoPeople(ctx context.Context, academic port.AcademicRepository, curricula port.CurriculumRepository, people port.PeopleRepository, slugs []string) (int, error) {
	total := 0
	for n, slug := range slugs {
		inst, err := academic.InstitutionBySlug(ctx, slug)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return total, err
		}
		existing, count, err := people.Students(ctx, domain.StudentFilter{InstitutionID: inst.ID, Limit: 1})
		if err != nil {
			return total, err
		}
		if count > 0 || len(existing) > 0 {
			continue
		}
		created, err := seedInstitution(ctx, inst, n, academic, curricula, people)
		total += created
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func seedInstitution(ctx context.Context, inst domain.Institution, n int, academic port.AcademicRepository, curricula port.CurriculumRepository, people port.PeopleRepository) (int, error) {
	rnd := rand.New(rand.NewPCG(uint64(n+1), 2026))
	levels, err := academic.Levels(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	levelByID := map[string]domain.Level{}
	gradePos := map[string]int{}
	for _, l := range levels {
		levelByID[l.ID] = l
		for i, g := range l.Grades {
			gradePos[g.ID] = i
		}
	}
	periods, err := academic.Periods(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	var running []domain.Period
	var history *domain.Period
	for i, p := range periods {
		if p.Status == domain.PeriodActive {
			running = append(running, p)
		}
		if p.Status == domain.PeriodClosed && inst.Kind == domain.KindEBR {
			history = &periods[i]
		}
	}
	// Institute: units of the plan in force by semester
	unitsByGrade := map[string][]string{}
	if inst.Kind == domain.KindInstitute {
		plans, err := curricula.Curricula(ctx, inst.ID)
		if err != nil {
			return 0, err
		}
		for _, p := range plans {
			if p.Status != domain.CurriculumActive {
				continue
			}
			full, err := curricula.Curriculum(ctx, inst.ID, p.ID)
			if err != nil {
				return 0, err
			}
			for _, s := range full.Subjects {
				for _, g := range s.GradeIDs {
					unitsByGrade[g] = append(unitsByGrade[g], s.ID)
				}
			}
		}
	}
	var historySections []domain.Section
	if history != nil {
		if historySections, err = academic.Sections(ctx, inst.ID, history.ID); err != nil {
			return 0, err
		}
	}

	dni := 70_000_000 + n*1_000_000 // outside the range of real DNIs of children
	nextDNI := func() string {
		dni += 1 + rnd.IntN(7)
		return strconv.Itoa(dni)
	}
	created := 0
	var lastFamily []domain.Guardian
	lastSurname := ""
	seen := map[string]bool{}
	for _, period := range running {
		// A student is in one running period: CEPRE's review cycle takes new students
		sections, err := academic.Sections(ctx, inst.ID, period.ID)
		if err != nil {
			return created, err
		}
		for _, sec := range sections {
			level := levelByID[sec.LevelID]
			fill := 0.65 + rnd.Float64()*0.27
			count := int(float64(sec.Capacity) * fill)
			for i := 0; i < count; i++ {
				female := rnd.IntN(2) == 0
				given := boyNames[rnd.IntN(len(boyNames))]
				if female {
					given = girlNames[rnd.IntN(len(girlNames))]
				}
				if rnd.IntN(3) == 0 {
					second := boyNames[rnd.IntN(len(boyNames))]
					if female {
						second = girlNames[rnd.IntN(len(girlNames))]
					}
					if second != given {
						given += " " + second
					}
				}
				sibling := len(lastFamily) > 0 && rnd.IntN(8) == 0 && inst.Kind != domain.KindEBA && inst.Kind != domain.KindInstitute
				paternal, maternal := demoSurnames[rnd.IntN(len(demoSurnames))], demoSurnames[rnd.IntN(len(demoSurnames))]
				if sibling {
					paternal = lastSurname
				}
				key := given + paternal + maternal
				if seen[key] {
					maternal = demoSurnames[(rnd.IntN(len(demoSurnames))+7)%len(demoSurnames)]
				}
				seen[given+paternal+maternal] = true
				age := ageFor(inst.Kind, level.Code, gradePos[sec.GradeID], rnd)
				birth := time.Date(2026-age-1, time.Month(4+rnd.IntN(9)), 1+rnd.IntN(28), 0, 0, 0, 0, time.UTC)
				sex := "m"
				if female {
					sex = "f"
				}
				in := domain.StudentInput{
					DocumentType: "dni", DocumentNumber: nextDNI(), GivenNames: given, PaternalSurname: paternal, MaternalSurname: maternal,
					BirthDate: &birth, Sex: sex, Status: domain.StudentActive, SpecialNeeds: inst.Kind == domain.KindEBE || rnd.IntN(40) == 0,
					Address: fmt.Sprintf("Jr. %s %d", demoSurnames[rnd.IntN(len(demoSurnames))], 100+rnd.IntN(900)),
				}
				adult := age >= 18
				if adult || inst.Kind == domain.KindCEPRE {
					in.Email = fmt.Sprintf("%s.%s%d@estudiantes.test", ascii(strings.Fields(given)[0]), ascii(paternal), rnd.IntN(90)+10)
					in.Phone = fmt.Sprintf("9%02d %03d %03d", rnd.IntN(100), rnd.IntN(1000), rnd.IntN(1000))
				}
				st, err := people.CreateStudent(ctx, inst.ID, in)
				if err != nil {
					return created, fmt.Errorf("student %s: %w", in.DocumentNumber, err)
				}
				created++

				// Guardians: minors have one or two; siblings share them; adults (EBA, institutes) have none
				if !adult || inst.Kind == domain.KindCEPRE {
					family := lastFamily
					if !sibling {
						family = nil
						mother := domain.GuardianInput{DocumentType: "dni", DocumentNumber: strconv.Itoa(10_000_000 + rnd.IntN(40_000_000)), GivenNames: adultFemale[rnd.IntN(len(adultFemale))], Surnames: maternal + " " + demoSurnames[rnd.IntN(len(demoSurnames))], Phone: fmt.Sprintf("9%02d %03d %03d", rnd.IntN(100), rnd.IntN(1000), rnd.IntN(1000)), Occupation: occupations[rnd.IntN(len(occupations))]}
						mother.Email = fmt.Sprintf("%s.%s%d@familias.test", ascii(mother.GivenNames), ascii(maternal), rnd.IntN(90)+10)
						g, err := people.CreateGuardian(ctx, inst.ID, mother)
						if err != nil {
							return created, fmt.Errorf("guardian: %w", err)
						}
						family = append(family, withLink(g, "mother", true))
						if rnd.IntN(3) != 0 {
							father := domain.GuardianInput{DocumentType: "dni", DocumentNumber: strconv.Itoa(10_000_000 + rnd.IntN(40_000_000)), GivenNames: adultMale[rnd.IntN(len(adultMale))], Surnames: paternal + " " + demoSurnames[rnd.IntN(len(demoSurnames))], Phone: fmt.Sprintf("9%02d %03d %03d", rnd.IntN(100), rnd.IntN(1000), rnd.IntN(1000)), Occupation: occupations[rnd.IntN(len(occupations))]}
							if g, err := people.CreateGuardian(ctx, inst.ID, father); err == nil {
								family = append(family, withLink(g, "father", false))
							}
						}
					}
					for _, g := range family {
						if err := people.LinkGuardian(ctx, inst.ID, st.ID, g.ID, domain.GuardianLink{Relationship: g.Relationship, IsPrimary: g.IsPrimary, CanPickUp: true, LivesWith: true}); err != nil {
							return created, fmt.Errorf("link: %w", err)
						}
					}
					lastFamily, lastSurname = family, paternal
				}

				// Enrollment in the running period, and in the school the previous year one grade below
				first := gradePos[sec.GradeID] == 0 && level.Position == 0
				kind := domain.EnrollContinuing
				if first || rnd.IntN(12) == 0 || inst.Kind == domain.KindCEPRE {
					kind = domain.EnrollNew
				}
				in2 := domain.EnrollmentInput{StudentID: st.ID, PeriodID: period.ID, LevelID: sec.LevelID, GradeID: sec.GradeID, SectionID: sec.ID, Kind: kind, EnrolledOn: period.StartsOn.AddDate(0, 0, -rnd.IntN(40)), UnitIDs: unitsByGrade[sec.GradeID]}
				if _, err := people.CreateEnrollment(ctx, inst.ID, in2); err != nil {
					return created, fmt.Errorf("enrollment: %w", err)
				}
				if history != nil && kind == domain.EnrollContinuing {
					prevLevel, prevGrade, ok := previousPlace(levels, sec.LevelID, sec.GradeID)
					if ok {
						var prevSection string
						for _, hs := range historySections {
							if hs.LevelID == prevLevel && hs.GradeID == prevGrade && hs.Name == sec.Name {
								prevSection = hs.ID
							}
						}
						old, err := people.CreateEnrollment(ctx, inst.ID, domain.EnrollmentInput{StudentID: st.ID, PeriodID: history.ID, LevelID: prevLevel, GradeID: prevGrade, SectionID: prevSection, Kind: domain.EnrollContinuing, EnrolledOn: history.StartsOn.AddDate(0, 0, -rnd.IntN(30))})
						if err != nil {
							return created, fmt.Errorf("history: %w", err)
						}
						if _, err := people.SetEnrollmentStatus(ctx, inst.ID, old.ID, domain.EnrollmentCompleted, nil, ""); err != nil {
							return created, err
						}
					}
				}
			}
		}
		// CEPRE students are in one cycle only
		if inst.Kind == domain.KindCEPRE {
			break
		}
	}
	return created, nil
}

func withLink(g domain.Guardian, relationship string, primary bool) domain.Guardian {
	g.Relationship, g.IsPrimary = relationship, primary
	return g
}

// previousPlace is the grade a continuing student came from (the inverse of nextPlace)
func previousPlace(levels []domain.Level, levelID, gradeID string) (string, string, bool) {
	for li, l := range levels {
		if l.ID != levelID {
			continue
		}
		for gi, g := range l.Grades {
			if g.ID != gradeID {
				continue
			}
			if gi > 0 {
				return l.ID, l.Grades[gi-1].ID, true
			}
			if li > 0 && len(levels[li-1].Grades) > 0 {
				prev := levels[li-1]
				return prev.ID, prev.Grades[len(prev.Grades)-1].ID, true
			}
		}
	}
	return "", "", false
}
