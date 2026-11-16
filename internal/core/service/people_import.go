package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"api/internal/core/domain"
)

// Loading the real roll of an institution (phase E9 of docs/ceba/PLAN.md): the secretariat uploads a spreadsheet
// (CSV) with one student per row and the cycle, grade and section of the period. A dry run checks every row with the
// same rules as enrolling by hand (age, authorized form, places) and reconciles the totals per section; the import
// runs only when the dry run has no errors, creating the students that are missing and enrolling them

const maxImportRows = 2000

// ImportRow is one line of the spreadsheet, as written there
type ImportRow struct {
	Line            int
	DocumentType    string
	DocumentNumber  string
	PaternalSurname string
	MaternalSurname string
	GivenNames      string
	BirthDate       string
	Sex             string
	Email           string
	Phone           string
	Cycle           string
	Grade           string
	Section         string
	Kind            string
	Origin          string
}

// Row outcomes
const (
	ImportReady    = "ready"
	ImportExisting = "existing"
	ImportEnrolled = "enrolled"
	ImportSkipped  = "skipped"
	ImportError    = "error"
)

type ImportResult struct {
	Line        int
	Status      string
	StudentName string
	Placement   string
	// The student already exists in the institution (found by document)
	Known   bool
	Message string
}

// ImportSection reconciles one section: enrolled before, rows of the file and the total after
type ImportSection struct {
	SectionID string
	Label     string
	Capacity  int
	Before    int
	Incoming  int
	After     int
}

type ImportReport struct {
	Rows      []ImportResult
	Sections  []ImportSection
	Total     int
	Ready     int
	Skipped   int
	Errors    int
	Created   int
	Enrolled  int
	Committed bool
	// Active enrollments of the period before and after
	PeriodBefore int
	PeriodAfter  int
}

var errImportHasErrors = domain.Validation(map[string]string{"rows": "Corrige las filas con error antes de importar."})

// foldName lowers a name and drops accents and dots, to compare "Ciclo Avanzado" with "avanzado" or "3.°" with "3"
func foldName(v string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(strings.TrimSpace(v))) {
		switch {
		case r >= 0x300 && r <= 0x36f, r == '.', r == '°', r == 'º':
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

var digits = regexp.MustCompile(`\d+`)

// sameGrade compares a written grade ("3", "3.°", "3.° grado", "Tercero") with a grade of the structure
func sameGrade(written string, g domain.Grade) bool {
	w := foldName(written)
	if w == "" {
		return false
	}
	if w == foldName(g.Name) || w == foldName(g.ShortName) || w == foldName(g.Code) {
		return true
	}
	ordinals := map[string]string{"primero": "1", "segundo": "2", "tercero": "3", "cuarto": "4", "quinto": "5", "sexto": "6"}
	if d, ok := ordinals[strings.Fields(w)[0]]; ok {
		w = d
	}
	return digits.FindString(w) != "" && digits.FindString(w) == digits.FindString(foldName(g.ShortName)+" "+foldName(g.Name))
}

func sameLevel(written string, l domain.Level) bool {
	w := strings.TrimPrefix(foldName(written), "ciclo ")
	return w != "" && (w == strings.TrimPrefix(foldName(l.Name), "ciclo ") || w == foldName(l.Code))
}

var importDocTypes = map[string]string{"dni": "dni", "": "dni", "ce": "ce", "carne de extranjeria": "ce", "pasaporte": "passport", "passport": "passport", "otro": "other", "other": "other"}

// importSex reads F/M/H as the SIAGIE writes it: M is mujer, H is hombre
var importSex = map[string]string{"": "", "f": "f", "mujer": "f", "femenino": "f", "m": "f", "h": "m", "hombre": "m", "masculino": "m", "v": "m", "varon": "m"}

var importKinds = map[string]string{"": domain.EnrollNew, "nuevo": domain.EnrollNew, "ingresante": domain.EnrollNew, "new": domain.EnrollNew,
	"promovido": domain.EnrollContinuing, "continuing": domain.EnrollContinuing, "continua": domain.EnrollContinuing,
	"trasladado": domain.EnrollTransferIn, "traslado": domain.EnrollTransferIn, "transfer_in": domain.EnrollTransferIn,
	"reentrante": domain.EnrollReEntry, "reincorporado": domain.EnrollReEntry, "re_entry": domain.EnrollReEntry}

func parseImportDate(v string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{"02/01/2006", "2/1/2006", time.DateOnly, "02-01-2006"} {
		if t, err := time.Parse(layout, v); err == nil {
			return &t, nil
		}
	}
	return nil, errors.New("fecha")
}

// plannedRow is a row checked and ready to apply
type plannedRow struct {
	result  int
	known   *domain.Student
	student domain.StudentInput
	enroll  domain.EnrollmentInput
}

// ImportRoll checks (commit=false) or imports (commit=true) the rows into periodID
func (s *PeopleService) ImportRoll(ctx context.Context, token, slug, periodID string, rows []ImportRow, commit bool) (ImportReport, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return ImportReport{}, err
	}
	if len(rows) == 0 || len(rows) > maxImportRows {
		return ImportReport{}, domain.Validation(map[string]string{"rows": fmt.Sprintf("El archivo debe tener entre 1 y %d filas.", maxImportRows)})
	}
	inst := sc.institution.ID
	period, err := s.academic.repo.Period(ctx, inst, periodID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ImportReport{}, domain.Validation(map[string]string{"periodId": "Elige el periodo."})
		}
		return ImportReport{}, err
	}
	if period.Status == domain.PeriodClosed {
		return ImportReport{}, domain.ErrPeriodClosed
	}
	levels, err := s.academic.repo.Levels(ctx, inst)
	if err != nil {
		return ImportReport{}, err
	}
	sections, err := s.academic.repo.Sections(ctx, inst, periodID)
	if err != nil {
		return ImportReport{}, err
	}
	occupancy, err := s.repo.Occupancy(ctx, inst, periodID)
	if err != nil {
		return ImportReport{}, err
	}
	report := ImportReport{Total: len(rows)}
	bySection := map[string]*ImportSection{}
	for _, o := range occupancy {
		report.PeriodBefore += o.Active
		i := slices.IndexFunc(sections, func(x domain.Section) bool { return x.ID == o.SectionID })
		if i < 0 {
			continue
		}
		x := sections[i]
		label := strings.TrimSpace(x.GradeShort+" "+x.Name) + " · " + x.LevelName
		bySection[o.SectionID] = &ImportSection{SectionID: o.SectionID, Label: label, Capacity: o.Capacity, Before: o.Active, After: o.Active}
	}
	today := s.now()
	seen := map[string]int{}
	var plan []plannedRow
	for _, row := range rows {
		res := ImportResult{Line: row.Line, Status: ImportReady}
		fail := func(msg string) {
			res.Status, res.Message = ImportError, msg
		}
		docType, ok := importDocTypes[foldName(row.DocumentType)]
		in := domain.StudentInput{DocumentType: docType, DocumentNumber: strings.TrimSpace(row.DocumentNumber), PaternalSurname: row.PaternalSurname,
			MaternalSurname: row.MaternalSurname, GivenNames: row.GivenNames, Email: strings.TrimSpace(row.Email), Phone: strings.TrimSpace(row.Phone)}
		res.StudentName = strings.TrimSpace(strings.Join(strings.Fields(row.PaternalSurname+" "+row.MaternalSurname), " ") + ", " + strings.TrimSpace(row.GivenNames))
		sex, sexOK := importSex[foldName(row.Sex)]
		in.Sex = sex
		birth, dateErr := parseImportDate(row.BirthDate)
		in.BirthDate = birth
		kind, kindOK := importKinds[foldName(row.Kind)]
		switch {
		case !ok:
			fail("Tipo de documento desconocido (DNI, CE, pasaporte u otro).")
		case !sexOK:
			fail("Sexo: escribe M (mujer) o H (hombre).")
		case dateErr != nil:
			fail("Fecha de nacimiento: usa dd/mm/aaaa.")
		case !kindOK:
			fail("Situación: nuevo, promovido, trasladado o reentrante.")
		}
		if res.Status == ImportReady {
			if err := s.validateStudent(&in); err != nil {
				fail(errMessage(err))
			}
		}
		key := in.DocumentType + ":" + in.DocumentNumber
		if res.Status == ImportReady {
			if first, dup := seen[key]; dup {
				fail(fmt.Sprintf("El documento se repite en la fila %d.", first))
			} else {
				seen[key] = row.Line
			}
		}
		// Where the student goes
		var level domain.Level
		var grade domain.Grade
		var section domain.Section
		if res.Status == ImportReady {
			li := slices.IndexFunc(levels, func(l domain.Level) bool { return l.IsActive && sameLevel(row.Cycle, l) })
			if li < 0 {
				fail("No hay un ciclo «" + strings.TrimSpace(row.Cycle) + "».")
			} else {
				level = levels[li]
				gi := slices.IndexFunc(level.Grades, func(g domain.Grade) bool { return sameGrade(row.Grade, g) })
				if gi < 0 {
					fail("No hay el grado «" + strings.TrimSpace(row.Grade) + "» en " + level.Name + ".")
				} else {
					grade = level.Grades[gi]
					si := slices.IndexFunc(sections, func(x domain.Section) bool {
						return x.LevelID == level.ID && foldName(x.Name) == foldName(row.Section) && (x.GradeID == grade.ID || x.Multigrade)
					})
					if si < 0 {
						fail("No hay la sección «" + strings.TrimSpace(row.Section) + "» de " + grade.ShortName + " " + level.Name + " en el periodo.")
					} else {
						section = sections[si]
						res.Placement = strings.TrimSpace(grade.ShortName+" "+section.Name) + " · " + level.Name
					}
				}
			}
		}
		// The student: already in the institution (by document), and maybe already enrolled in the period
		var known *domain.Student
		if res.Status == ImportReady {
			st, err := s.repo.StudentByDocument(ctx, inst, in.DocumentType, in.DocumentNumber)
			switch {
			case err == nil:
				known, res.Known = &st, true
				if _, err := s.repo.EnrollmentOf(ctx, inst, st.ID, periodID); err == nil {
					res.Status, res.Message = ImportSkipped, "Ya está matriculado en el periodo."
				} else if !errors.Is(err, domain.ErrNotFound) {
					return report, err
				} else if st.Status != domain.StudentActive {
					fail("El estudiante existe pero no está activo.")
				} else {
					res.Status = ImportExisting
				}
			case errors.Is(err, domain.ErrNotFound):
			default:
				return report, err
			}
		}
		if res.Status == ImportReady || res.Status == ImportExisting {
			check := domain.Student{BirthDate: in.BirthDate}
			if known != nil && known.BirthDate != nil {
				check.BirthDate = known.BirthDate
			}
			if err := s.checkAge(ctx, sc, check, section.ID, today); err != nil {
				fail(errMessage(err))
			} else if kind == domain.EnrollTransferIn && strings.TrimSpace(row.Origin) == "" {
				fail("Un traslado indica de qué institución viene.")
			} else if _, _, err := s.placement(ctx, sc, periodID, level.ID, grade.ID, section.ID, ""); err != nil && !errors.Is(err, domain.ErrSectionFull) {
				fail(errMessage(err))
			}
		}
		// Places: what the section already holds plus the rows before this one
		if res.Status == ImportReady || res.Status == ImportExisting {
			sum := bySection[section.ID]
			if sum == nil {
				sum = &ImportSection{SectionID: section.ID, Label: res.Placement, Capacity: section.Capacity}
				bySection[section.ID] = sum
			}
			if sum.After >= sum.Capacity {
				fail(fmt.Sprintf("La sección se queda sin vacantes (%d).", sum.Capacity))
			} else {
				sum.Incoming++
				sum.After++
				plan = append(plan, plannedRow{result: len(report.Rows), known: known, student: in,
					enroll: domain.EnrollmentInput{PeriodID: periodID, LevelID: level.ID, GradeID: grade.ID, SectionID: section.ID, Kind: kind, EnrolledOn: today,
						Origin: strings.TrimSpace(row.Origin)}})
			}
		}
		switch res.Status {
		case ImportError:
			report.Errors++
		case ImportSkipped:
			report.Skipped++
		default:
			report.Ready++
		}
		report.Rows = append(report.Rows, res)
	}
	for _, x := range bySection {
		if x.Incoming > 0 {
			report.Sections = append(report.Sections, *x)
		}
	}
	slices.SortFunc(report.Sections, func(a, b ImportSection) int { return strings.Compare(a.Label, b.Label) })
	report.PeriodAfter = report.PeriodBefore + report.Ready
	if !commit {
		return report, nil
	}
	if report.Errors > 0 {
		return report, errImportHasErrors
	}
	// Apply: create who is missing, enroll everyone ready (each enrollment checks its rules again)
	for _, p := range plan {
		res := &report.Rows[p.result]
		studentID := ""
		if p.known != nil {
			studentID = p.known.ID
		} else {
			st, err := s.repo.CreateStudent(ctx, inst, p.student)
			if err != nil {
				res.Status, res.Message = ImportError, errMessage(err)
				report.Errors++
				continue
			}
			studentID = st.ID
			report.Created++
		}
		p.enroll.StudentID = studentID
		if _, err := s.enroll(ctx, sc, p.enroll); err != nil {
			res.Status, res.Message = ImportError, errMessage(err)
			report.Errors++
			continue
		}
		res.Status = ImportEnrolled
		report.Enrolled++
	}
	report.Committed = true
	if occupancy, err = s.repo.Occupancy(ctx, inst, periodID); err == nil {
		report.PeriodAfter = 0
		for _, o := range occupancy {
			report.PeriodAfter += o.Active
		}
		for i := range report.Sections {
			for _, o := range occupancy {
				if o.SectionID == report.Sections[i].SectionID {
					report.Sections[i].After = o.Active
				}
			}
		}
	}
	s.record(ctx, sc, AuditEntry{Action: domain.AuditCreate, Entity: "roll_import", EntityID: periodID,
		Summary: fmt.Sprintf("Importó la nómina de %s: %d matriculados (%d estudiantes nuevos), %d ya estaban", period.Name, report.Enrolled, report.Created, report.Skipped)})
	s.changed(ctx, sc, "enrollment")
	return report, nil
}

// errMessage is what a row tells the secretariat: the first field message of a validation, or the error's message
func errMessage(err error) string {
	var de *domain.Error
	if !errors.As(err, &de) {
		return "No se pudo procesar la fila."
	}
	if len(de.Fields) > 0 {
		keys := make([]string, 0, len(de.Fields))
		for k := range de.Fields {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		return de.Fields[keys[0]]
	}
	return de.Message
}
