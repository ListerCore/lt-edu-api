package service

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
	"api/internal/core/port"
)

var (
	// Secretaría runs enrollment; dirección and coordinación too
	peopleManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true, "secretary": true}
	peopleViewers  = map[string]bool{"program_coordinator": true, "special_ed_specialist": true, "teacher": true, "tutor": true, "assistant": true, "psychologist": true, "treasurer": true}

	validDocTypes      = map[string]bool{"dni": true, "ce": true, "passport": true, "other": true}
	validRelationships = map[string]bool{"mother": true, "father": true, "legal_guardian": true, "grandparent": true, "sibling": true, "other": true}
	validStudentStatus = map[string]bool{domain.StudentActive: true, domain.StudentInactive: true, domain.StudentGraduated: true, domain.StudentWithdrawn: true}
	validEnrollKinds   = map[string]bool{domain.EnrollNew: true, domain.EnrollContinuing: true, domain.EnrollTransferIn: true, domain.EnrollReEntry: true}

	dniPattern        = regexp.MustCompile(`^[0-9]{8}$`)
	otherDocPattern   = regexp.MustCompile(`^[A-Za-z0-9-]{4,20}$`)
	emailPattern      = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	phonePattern      = regexp.MustCompile(`^[0-9 +()-]{6,20}$`)
	studentCodePatern = regexp.MustCompile(`^[A-Za-z0-9-]{1,20}$`)
)

const EventPeopleChanged = "people.changed" // {institution, area}: students, guardians, enrollments or placements changed

// PeopleService runs the students and guardians of each institution and their enrollment in periods (phase 3 of
// docs/plan-academico.md). Secretaría, dirección and coordinación change; the rest of the staff reads
type PeopleService struct {
	repo      port.PeopleRepository
	curricula port.CurriculumRepository
	academic  *AcademicService
	now       func() time.Time
}

func NewPeopleService(repo port.PeopleRepository, curricula port.CurriculumRepository, academic *AcademicService) *PeopleService {
	return &PeopleService{repo: repo, curricula: curricula, academic: academic, now: time.Now}
}

func (s *PeopleService) scope(ctx context.Context, token, slug string, write bool) (academicScope, error) {
	sc, err := resolveInstitution(ctx, s.academic.auth, s.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return academicScope{}, err
	}
	switch {
	case peopleManagers[sc.role]:
	case peopleViewers[sc.role]:
		if write {
			return academicScope{}, domain.ErrPeopleReadOnly
		}
	default:
		return academicScope{}, domain.ErrPeopleForbidden
	}
	return academicScope{institutionScope: sc, profile: profileFor(sc.institution.Kind)}, nil
}

func (s *PeopleService) changed(ctx context.Context, sc academicScope, area string) {
	s.academic.live.Notify(ctx, EventPeopleChanged, map[string]string{"institution": sc.institution.Slug, "area": area})
}

// PeopleContext is what the people and enrollment pages need around their data: the institution's names and
// rules, its structure and its periods (and whether the person may change things)
type PeopleContext struct {
	Academic  AcademicContext
	CanManage bool
	Levels    []domain.Level
	Periods   []domain.Period
}

func (s *PeopleService) Context(ctx context.Context, token, slug string) (PeopleContext, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return PeopleContext{}, err
	}
	actx, err := s.academic.contextFor(ctx, sc)
	if err != nil {
		return PeopleContext{}, err
	}
	levels, err := s.academic.repo.Levels(ctx, sc.institution.ID)
	if err != nil {
		return PeopleContext{}, err
	}
	periods, err := s.academic.repo.Periods(ctx, sc.institution.ID)
	if err != nil {
		return PeopleContext{}, err
	}
	return PeopleContext{Academic: actx, CanManage: peopleManagers[sc.role], Levels: levels, Periods: periods}, nil
}

// ---------- students ----------

func (s *PeopleService) Students(ctx context.Context, token, slug string, f domain.StudentFilter) ([]domain.Student, int, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return nil, 0, err
	}
	f.InstitutionID = sc.institution.ID
	if f.Status != "" && !validStudentStatus[f.Status] {
		f.Status = ""
	}
	return s.repo.Students(ctx, f)
}

// StudentDetail is one student with everything around it
type StudentDetail struct {
	Student     domain.Student
	Guardians   []domain.Guardian
	Enrollments []domain.Enrollment
	Placements  []domain.Placement
	CanManage   bool
}

func (s *PeopleService) Student(ctx context.Context, token, slug, id string) (StudentDetail, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return StudentDetail{}, err
	}
	return s.studentDetail(ctx, sc, id)
}

func (s *PeopleService) studentDetail(ctx context.Context, sc academicScope, id string) (StudentDetail, error) {
	st, err := s.repo.Student(ctx, sc.institution.ID, id)
	if err != nil {
		return StudentDetail{}, err
	}
	guardians, err := s.repo.StudentGuardians(ctx, sc.institution.ID, id)
	if err != nil {
		return StudentDetail{}, err
	}
	enrollments, err := s.repo.StudentEnrollments(ctx, sc.institution.ID, id)
	if err != nil {
		return StudentDetail{}, err
	}
	placements, err := s.repo.Placements(ctx, sc.institution.ID, id)
	if err != nil {
		return StudentDetail{}, err
	}
	return StudentDetail{Student: st, Guardians: guardians, Enrollments: enrollments, Placements: placements, CanManage: peopleManagers[sc.role]}, nil
}

func validateDocument(fields map[string]string, docType, number *string) {
	*docType = strings.TrimSpace(*docType)
	*number = strings.ToUpper(strings.TrimSpace(*number))
	switch {
	case !validDocTypes[*docType]:
		fields["documentType"] = "Elige el tipo de documento."
	case *docType == "dni" && !dniPattern.MatchString(*number):
		fields["documentNumber"] = "El DNI tiene 8 dígitos."
	case *docType != "dni" && !otherDocPattern.MatchString(*number):
		fields["documentNumber"] = "Usa de 4 a 20 letras o números."
	}
}

func validateContact(fields map[string]string, email, phone *string) {
	*email = strings.ToLower(strings.TrimSpace(*email))
	*phone = strings.TrimSpace(*phone)
	if *email != "" && (!emailPattern.MatchString(*email) || len(*email) > 254) {
		fields["email"] = "Escribe un correo válido."
	}
	if *phone != "" && !phonePattern.MatchString(*phone) {
		fields["phone"] = "Escribe un teléfono válido."
	}
}

func (s *PeopleService) validateStudent(in *domain.StudentInput) error {
	fields := map[string]string{}
	in.GivenNames, in.PaternalSurname, in.MaternalSurname = strings.TrimSpace(in.GivenNames), strings.TrimSpace(in.PaternalSurname), strings.TrimSpace(in.MaternalSurname)
	in.Code, in.Address, in.Notes = strings.TrimSpace(in.Code), strings.TrimSpace(in.Address), strings.TrimSpace(in.Notes)
	if n := utf8.RuneCountInString(in.GivenNames); n < 2 || n > 80 {
		fields["givenNames"] = "Escribe los nombres."
	}
	if n := utf8.RuneCountInString(in.PaternalSurname); n < 2 || n > 60 {
		fields["paternalSurname"] = "Escribe el apellido paterno."
	}
	if utf8.RuneCountInString(in.MaternalSurname) > 60 {
		fields["maternalSurname"] = "Máximo 60 caracteres."
	}
	if in.Code != "" && !studentCodePatern.MatchString(in.Code) {
		fields["code"] = "Usa hasta 20 letras, números o guiones."
	}
	validateDocument(fields, &in.DocumentType, &in.DocumentNumber)
	validateContact(fields, &in.Email, &in.Phone)
	if in.BirthDate != nil {
		if in.BirthDate.After(s.now()) || in.BirthDate.Year() < 1920 {
			fields["birthDate"] = "Revisa la fecha de nacimiento."
		}
	}
	if in.Sex != "" && in.Sex != "f" && in.Sex != "m" {
		fields["sex"] = "Elige una opción."
	}
	if in.Status == "" {
		in.Status = domain.StudentActive
	}
	if !validStudentStatus[in.Status] {
		fields["status"] = "Elige un estado válido."
	}
	if utf8.RuneCountInString(in.Address) > 200 {
		fields["address"] = "Máximo 200 caracteres."
	}
	if utf8.RuneCountInString(in.Notes) > 1000 {
		fields["notes"] = "Máximo 1000 caracteres."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

func (s *PeopleService) CreateStudent(ctx context.Context, token, slug string, in domain.StudentInput) (domain.Student, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Student{}, err
	}
	if err := s.validateStudent(&in); err != nil {
		return domain.Student{}, err
	}
	st, err := s.repo.CreateStudent(ctx, sc.institution.ID, in)
	if err == nil {
		s.changed(ctx, sc, "students")
	}
	return st, err
}

func (s *PeopleService) UpdateStudent(ctx context.Context, token, slug, id string, in domain.StudentInput) (domain.Student, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Student{}, err
	}
	if err := s.validateStudent(&in); err != nil {
		return domain.Student{}, err
	}
	if in.Code == "" {
		current, err := s.repo.Student(ctx, sc.institution.ID, id)
		if err != nil {
			return domain.Student{}, err
		}
		in.Code = current.Code
	}
	st, err := s.repo.UpdateStudent(ctx, sc.institution.ID, id, in)
	if err == nil {
		s.changed(ctx, sc, "students")
	}
	return st, err
}

func (s *PeopleService) DeleteStudent(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteStudent(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "students")
	return nil
}

// ---------- guardians ----------

func validateGuardian(in *domain.GuardianInput) map[string]string {
	fields := map[string]string{}
	in.GivenNames, in.Surnames, in.Occupation = strings.TrimSpace(in.GivenNames), strings.TrimSpace(in.Surnames), strings.TrimSpace(in.Occupation)
	if n := utf8.RuneCountInString(in.GivenNames); n < 2 || n > 80 {
		fields["givenNames"] = "Escribe los nombres."
	}
	if n := utf8.RuneCountInString(in.Surnames); n < 2 || n > 120 {
		fields["surnames"] = "Escribe los apellidos."
	}
	if utf8.RuneCountInString(in.Occupation) > 80 {
		fields["occupation"] = "Máximo 80 caracteres."
	}
	validateDocument(fields, &in.DocumentType, &in.DocumentNumber)
	validateContact(fields, &in.Email, &in.Phone)
	return fields
}

func validateLink(fields map[string]string, link domain.GuardianLink) {
	if !validRelationships[link.Relationship] {
		fields["relationship"] = "Elige el parentesco."
	}
}

// FindGuardian looks a guardian up by document, to link a sibling's guardian instead of typing it again
func (s *PeopleService) FindGuardian(ctx context.Context, token, slug, docType, number string) (domain.Guardian, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return domain.Guardian{}, err
	}
	return s.repo.GuardianByDocument(ctx, sc.institution.ID, strings.TrimSpace(docType), strings.ToUpper(strings.TrimSpace(number)))
}

// AddGuardian links an existing guardian (guardianID) or a new one (in) to the student. A new one whose document
// already exists in the institution is linked as it is, so siblings share their guardians
func (s *PeopleService) AddGuardian(ctx context.Context, token, slug, studentID, guardianID string, in domain.GuardianInput, link domain.GuardianLink) (StudentDetail, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return StudentDetail{}, err
	}
	fields := map[string]string{}
	validateLink(fields, link)
	if guardianID == "" {
		for k, v := range validateGuardian(&in) {
			fields[k] = v
		}
	}
	if len(fields) > 0 {
		return StudentDetail{}, domain.Validation(fields)
	}
	if _, err := s.repo.Student(ctx, sc.institution.ID, studentID); err != nil {
		return StudentDetail{}, err
	}
	if guardianID == "" {
		existing, err := s.repo.GuardianByDocument(ctx, sc.institution.ID, in.DocumentType, in.DocumentNumber)
		switch {
		case err == nil:
			guardianID = existing.ID
		case errors.Is(err, domain.ErrNotFound):
			created, err := s.repo.CreateGuardian(ctx, sc.institution.ID, in)
			if err != nil {
				return StudentDetail{}, err
			}
			guardianID = created.ID
		default:
			return StudentDetail{}, err
		}
	}
	linked, err := s.repo.IsLinked(ctx, studentID, guardianID)
	if err != nil {
		return StudentDetail{}, err
	}
	if linked {
		return StudentDetail{}, domain.ErrGuardianLinkExists
	}
	if err := s.repo.LinkGuardian(ctx, sc.institution.ID, studentID, guardianID, link); err != nil {
		return StudentDetail{}, err
	}
	s.changed(ctx, sc, "students")
	return s.studentDetail(ctx, sc, studentID)
}

// UpdateGuardian edits the person (for every student it answers for) and its link with this student
func (s *PeopleService) UpdateGuardian(ctx context.Context, token, slug, studentID, guardianID string, in domain.GuardianInput, link domain.GuardianLink) (StudentDetail, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return StudentDetail{}, err
	}
	fields := validateGuardian(&in)
	validateLink(fields, link)
	if len(fields) > 0 {
		return StudentDetail{}, domain.Validation(fields)
	}
	linked, err := s.repo.IsLinked(ctx, studentID, guardianID)
	if err != nil {
		return StudentDetail{}, err
	}
	if !linked {
		return StudentDetail{}, domain.ErrNotFound
	}
	if _, err := s.repo.UpdateGuardian(ctx, sc.institution.ID, guardianID, in); err != nil {
		return StudentDetail{}, err
	}
	if err := s.repo.LinkGuardian(ctx, sc.institution.ID, studentID, guardianID, link); err != nil {
		return StudentDetail{}, err
	}
	s.changed(ctx, sc, "students")
	return s.studentDetail(ctx, sc, studentID)
}

func (s *PeopleService) RemoveGuardian(ctx context.Context, token, slug, studentID, guardianID string) (StudentDetail, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return StudentDetail{}, err
	}
	if err := s.repo.UnlinkGuardian(ctx, sc.institution.ID, studentID, guardianID); err != nil {
		return StudentDetail{}, err
	}
	s.changed(ctx, sc, "students")
	return s.studentDetail(ctx, sc, studentID)
}

// GrantAccess gives the student (role student) or a guardian (role guardian) the panel with its registered email
func (s *PeopleService) GrantAccess(ctx context.Context, token, slug, studentID, guardianID string) (StudentDetail, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return StudentDetail{}, err
	}
	email, role, access := "", "student", ""
	if guardianID == "" {
		st, err := s.repo.Student(ctx, sc.institution.ID, studentID)
		if err != nil {
			return StudentDetail{}, err
		}
		email, access = st.Email, st.Access
	} else {
		linked, err := s.repo.IsLinked(ctx, studentID, guardianID)
		if err != nil {
			return StudentDetail{}, err
		}
		if !linked {
			return StudentDetail{}, domain.ErrNotFound
		}
		g, err := s.repo.Guardian(ctx, sc.institution.ID, guardianID)
		if err != nil {
			return StudentDetail{}, err
		}
		email, role, access = g.Email, "guardian", g.Access
	}
	if email == "" {
		return StudentDetail{}, domain.ErrNoEmailForAccess
	}
	if access == domain.AccessAccount {
		return StudentDetail{}, domain.ErrAlreadyHasAccess
	}
	if _, err := s.repo.GrantAccess(ctx, sc.institution.ID, email, role, sc.userID); err != nil {
		return StudentDetail{}, err
	}
	s.changed(ctx, sc, "students")
	return s.studentDetail(ctx, sc, studentID)
}

// ---------- enrollment ----------

// EnrollmentBoard is a period's enrollment: its sections with their occupancy and the enrolled students
type EnrollmentBoard struct {
	Period      domain.Period
	Sections    []domain.Section
	Occupancy   []domain.SectionOccupancy
	Enrollments []domain.Enrollment
	Total       int
	// Institutes: the units of the active plan of every program, to pick from when enrolling
	Units []domain.Subject
}

func (s *PeopleService) Board(ctx context.Context, token, slug string, f domain.EnrollmentFilter) (EnrollmentBoard, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return EnrollmentBoard{}, err
	}
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, f.PeriodID)
	if err != nil {
		return EnrollmentBoard{}, err
	}
	sections, err := s.academic.repo.Sections(ctx, sc.institution.ID, p.ID)
	if err != nil {
		return EnrollmentBoard{}, err
	}
	occupancy, err := s.repo.Occupancy(ctx, sc.institution.ID, p.ID)
	if err != nil {
		return EnrollmentBoard{}, err
	}
	f.InstitutionID = sc.institution.ID
	enrollments, total, err := s.repo.Enrollments(ctx, f)
	if err != nil {
		return EnrollmentBoard{}, err
	}
	board := EnrollmentBoard{Period: p, Sections: sections, Occupancy: occupancy, Enrollments: enrollments, Total: total}
	if sc.profile.usesFormativeLevels {
		board.Units, err = s.activeUnits(ctx, sc, "")
		if err != nil {
			return EnrollmentBoard{}, err
		}
	}
	return board, nil
}

// activeUnits returns the didactic units and EFSRT of the plans in force (of one level, or all)
func (s *PeopleService) activeUnits(ctx context.Context, sc academicScope, levelID string) ([]domain.Subject, error) {
	plans, err := s.curricula.Curricula(ctx, sc.institution.ID)
	if err != nil {
		return nil, err
	}
	var units []domain.Subject
	for _, p := range plans {
		if p.Status != domain.CurriculumActive || (levelID != "" && p.LevelID != levelID) {
			continue
		}
		full, err := s.curricula.Curriculum(ctx, sc.institution.ID, p.ID)
		if err != nil {
			return nil, err
		}
		for _, sub := range full.Subjects {
			if sub.Kind == domain.SubjectUnit || sub.Kind == domain.SubjectEFSRT {
				sub.CurriculumID = p.LevelID // carries the program for the panel
				units = append(units, sub)
			}
		}
	}
	return units, nil
}

// placement checks where an enrollment goes: the period is open, grade and section match and the section has room
func (s *PeopleService) placement(ctx context.Context, sc academicScope, periodID, levelID, gradeID, sectionID, enrollmentID string) (domain.Period, domain.Level, error) {
	fields := map[string]string{}
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Period{}, domain.Level{}, domain.Validation(map[string]string{"periodId": "Elige el periodo."})
		}
		return domain.Period{}, domain.Level{}, err
	}
	if p.Status == domain.PeriodClosed {
		return domain.Period{}, domain.Level{}, domain.ErrPeriodClosed
	}
	level, err := s.academic.repo.Level(ctx, sc.institution.ID, levelID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Period{}, domain.Level{}, domain.Validation(map[string]string{"levelId": "Elige un " + strings.ToLower(sc.profile.vocabulary.Level) + "."})
		}
		return domain.Period{}, domain.Level{}, err
	}
	if sc.profile.hasGrades {
		if !slices.ContainsFunc(level.Grades, func(g domain.Grade) bool { return g.ID == gradeID }) {
			fields["gradeId"] = "Elige un " + strings.ToLower(sc.profile.vocabulary.Grade) + " de " + level.Name + "."
		}
	} else if gradeID != "" {
		fields["gradeId"] = "Este tipo de institución no usa grados."
	}
	if sectionID != "" {
		sections, err := s.academic.repo.Sections(ctx, sc.institution.ID, periodID)
		if err != nil {
			return domain.Period{}, domain.Level{}, err
		}
		i := slices.IndexFunc(sections, func(x domain.Section) bool { return x.ID == sectionID })
		switch {
		case i < 0:
			fields["sectionId"] = "Esa " + strings.ToLower(sc.profile.vocabulary.Section) + " no es de este periodo."
		case sections[i].LevelID != levelID || sections[i].GradeID != gradeID:
			fields["sectionId"] = "Esa " + strings.ToLower(sc.profile.vocabulary.Section) + " no corresponde a lo elegido."
		default:
			occupancy, err := s.repo.Occupancy(ctx, sc.institution.ID, periodID)
			if err != nil {
				return domain.Period{}, domain.Level{}, err
			}
			for _, o := range occupancy {
				if o.SectionID == sectionID && o.Active >= o.Capacity {
					// Moving inside the same section never counts against it
					if enrollmentID != "" {
						if current, err := s.repo.Enrollment(ctx, sc.institution.ID, enrollmentID); err == nil && current.SectionID == sectionID && current.Status == domain.EnrollmentActive {
							continue
						}
					}
					return domain.Period{}, domain.Level{}, domain.ErrSectionFull
				}
			}
		}
	}
	if len(fields) > 0 {
		return domain.Period{}, domain.Level{}, domain.Validation(fields)
	}
	return p, level, nil
}

// units validates the units of an institute enrollment (empty: every unit of its semester in the plan in force)
func (s *PeopleService) units(ctx context.Context, sc academicScope, levelID, gradeID string, unitIDs []string) ([]string, error) {
	if !sc.profile.usesFormativeLevels {
		return nil, nil
	}
	available, err := s.activeUnits(ctx, sc, levelID)
	if err != nil {
		return nil, err
	}
	if len(available) == 0 {
		return nil, &domain.Error{Kind: domain.KindInvalid, Code: "no_active_plan", Message: "El programa no tiene un plan de estudios vigente. Pon uno en vigencia para matricular por unidades."}
	}
	unitIDs = unique(unitIDs)
	if len(unitIDs) == 0 {
		for _, u := range available {
			if slices.Contains(u.GradeIDs, gradeID) {
				unitIDs = append(unitIDs, u.ID)
			}
		}
		return unitIDs, nil
	}
	for _, id := range unitIDs {
		if !slices.ContainsFunc(available, func(u domain.Subject) bool { return u.ID == id }) {
			return nil, domain.Validation(map[string]string{"unitIds": "Una de las unidades no es del plan vigente del programa."})
		}
	}
	return unitIDs, nil
}

func (s *PeopleService) Enroll(ctx context.Context, token, slug string, in domain.EnrollmentInput) (domain.Enrollment, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Enrollment{}, err
	}
	return s.enroll(ctx, sc, in)
}

func (s *PeopleService) enroll(ctx context.Context, sc academicScope, in domain.EnrollmentInput) (domain.Enrollment, error) {
	fields := map[string]string{}
	if in.Kind == "" {
		in.Kind = domain.EnrollNew
	}
	if !validEnrollKinds[in.Kind] {
		fields["kind"] = "Elige el tipo de matrícula."
	}
	in.Origin, in.Notes = strings.TrimSpace(in.Origin), strings.TrimSpace(in.Notes)
	if in.Kind == domain.EnrollTransferIn && in.Origin == "" {
		fields["origin"] = "Indica de qué institución viene."
	}
	if utf8.RuneCountInString(in.Origin) > 160 || utf8.RuneCountInString(in.Notes) > 1000 {
		fields["notes"] = "El texto es demasiado largo."
	}
	if in.EnrolledOn.IsZero() {
		in.EnrolledOn = s.now()
	}
	if len(fields) > 0 {
		return domain.Enrollment{}, domain.Validation(fields)
	}
	st, err := s.repo.Student(ctx, sc.institution.ID, in.StudentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Enrollment{}, domain.Validation(map[string]string{"studentId": "Elige un estudiante."})
		}
		return domain.Enrollment{}, err
	}
	if st.Status != domain.StudentActive {
		return domain.Enrollment{}, domain.ErrStudentNotActive
	}
	if _, err := s.repo.EnrollmentOf(ctx, sc.institution.ID, in.StudentID, in.PeriodID); err == nil {
		return domain.Enrollment{}, domain.ErrAlreadyEnrolled
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Enrollment{}, err
	}
	if _, _, err := s.placement(ctx, sc, in.PeriodID, in.LevelID, in.GradeID, in.SectionID, ""); err != nil {
		return domain.Enrollment{}, err
	}
	if in.UnitIDs, err = s.units(ctx, sc, in.LevelID, in.GradeID, in.UnitIDs); err != nil {
		return domain.Enrollment{}, err
	}
	e, err := s.repo.CreateEnrollment(ctx, sc.institution.ID, in)
	if err == nil {
		s.changed(ctx, sc, "enrollment")
	}
	return e, err
}

// UpdateEnrollment moves a student to another grade or section of the same level and period (and, in institutes,
// changes its units)
func (s *PeopleService) UpdateEnrollment(ctx context.Context, token, slug, id, gradeID, sectionID, notes string, unitIDs []string, setUnits bool) (domain.Enrollment, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Enrollment{}, err
	}
	current, err := s.repo.Enrollment(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.Enrollment{}, err
	}
	if _, _, err := s.placement(ctx, sc, current.PeriodID, current.LevelID, gradeID, sectionID, id); err != nil {
		return domain.Enrollment{}, err
	}
	notes = strings.TrimSpace(notes)
	if utf8.RuneCountInString(notes) > 1000 {
		return domain.Enrollment{}, domain.Validation(map[string]string{"notes": "Máximo 1000 caracteres."})
	}
	var units []string
	if setUnits && sc.profile.usesFormativeLevels {
		if units, err = s.units(ctx, sc, current.LevelID, gradeID, unitIDs); err != nil {
			return domain.Enrollment{}, err
		}
		if units == nil {
			units = []string{}
		}
	}
	e, err := s.repo.UpdateEnrollment(ctx, sc.institution.ID, id, gradeID, sectionID, notes, units)
	if err == nil {
		s.changed(ctx, sc, "enrollment")
	}
	return e, err
}

// EndEnrollment records that the student left (withdrawn) or moved to another institution (transferred);
// status active brings the enrollment back
func (s *PeopleService) EndEnrollment(ctx context.Context, token, slug, id, status string, endedOn time.Time, destination string) (domain.Enrollment, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Enrollment{}, err
	}
	current, err := s.repo.Enrollment(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.Enrollment{}, err
	}
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, current.PeriodID)
	if err != nil {
		return domain.Enrollment{}, err
	}
	if p.Status == domain.PeriodClosed {
		return domain.Enrollment{}, domain.ErrPeriodClosed
	}
	destination = strings.TrimSpace(destination)
	switch status {
	case domain.EnrollmentActive:
		if current.SectionID != "" {
			if _, _, err := s.placement(ctx, sc, current.PeriodID, current.LevelID, current.GradeID, current.SectionID, ""); err != nil {
				return domain.Enrollment{}, err
			}
		}
		e, err := s.repo.SetEnrollmentStatus(ctx, sc.institution.ID, id, status, nil, "")
		if err == nil {
			s.changed(ctx, sc, "enrollment")
		}
		return e, err
	case domain.EnrollmentWithdrawn, domain.EnrollmentTransferred:
	default:
		return domain.Enrollment{}, domain.Validation(map[string]string{"status": "Elige retiro o traslado."})
	}
	fields := map[string]string{}
	if endedOn.IsZero() {
		endedOn = s.now()
	}
	if endedOn.Before(current.EnrolledOn) {
		fields["endedOn"] = "No puede ser antes de la matrícula."
	}
	if status == domain.EnrollmentTransferred && destination == "" {
		fields["destination"] = "Indica a qué institución se traslada."
	}
	if utf8.RuneCountInString(destination) > 160 {
		fields["destination"] = "Máximo 160 caracteres."
	}
	if len(fields) > 0 {
		return domain.Enrollment{}, domain.Validation(fields)
	}
	e, err := s.repo.SetEnrollmentStatus(ctx, sc.institution.ID, id, status, &endedOn, destination)
	if err == nil {
		s.changed(ctx, sc, "enrollment")
	}
	return e, err
}

func (s *PeopleService) DeleteEnrollment(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	current, err := s.repo.Enrollment(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if p, err := s.academic.repo.Period(ctx, sc.institution.ID, current.PeriodID); err != nil {
		return err
	} else if p.Status == domain.PeriodClosed {
		return domain.ErrPeriodClosed
	}
	if err := s.repo.DeleteEnrollment(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "enrollment")
	return nil
}

// RatifyResult counts what ratifying the continuing students did (or would do, in a preview)
type RatifyResult struct {
	Enrolled       int
	WithoutSection int
	Already        int
	Finishing      int
	Inactive       int
}

// Ratify enrolls the students of a previous period in the next grade (the next level's first grade after the last
// one; CEPRE: the same area), in the section with the same name when it exists and has room. Students in the last
// grade of the last level finish. With preview it only counts
func (s *PeopleService) Ratify(ctx context.Context, token, slug, fromPeriodID, toPeriodID string, preview bool) (RatifyResult, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return RatifyResult{}, err
	}
	if fromPeriodID == toPeriodID {
		return RatifyResult{}, domain.Validation(map[string]string{"fromPeriodId": "Elige un periodo distinto."})
	}
	target, err := s.academic.repo.Period(ctx, sc.institution.ID, toPeriodID)
	if err != nil {
		return RatifyResult{}, err
	}
	if target.Status == domain.PeriodClosed {
		return RatifyResult{}, domain.ErrPeriodClosed
	}
	if _, err := s.academic.repo.Period(ctx, sc.institution.ID, fromPeriodID); err != nil {
		return RatifyResult{}, err
	}
	levels, err := s.academic.repo.Levels(ctx, sc.institution.ID)
	if err != nil {
		return RatifyResult{}, err
	}
	sections, err := s.academic.repo.Sections(ctx, sc.institution.ID, toPeriodID)
	if err != nil {
		return RatifyResult{}, err
	}
	occupancy, err := s.repo.Occupancy(ctx, sc.institution.ID, toPeriodID)
	if err != nil {
		return RatifyResult{}, err
	}
	room := map[string]int{}
	for _, o := range occupancy {
		room[o.SectionID] = o.Capacity - o.Active
	}
	// The whole source period, page by page
	var source []domain.Enrollment
	for offset := 0; ; offset += 500 {
		page, _, err := s.repo.Enrollments(ctx, domain.EnrollmentFilter{InstitutionID: sc.institution.ID, PeriodID: fromPeriodID, Limit: 500, Offset: offset})
		if err != nil {
			return RatifyResult{}, err
		}
		source = append(source, page...)
		if len(page) < 500 {
			break
		}
	}
	sort.SliceStable(levels, func(i, j int) bool { return levels[i].Position < levels[j].Position })
	var res RatifyResult
	for _, e := range source {
		if e.Status == domain.EnrollmentWithdrawn || e.Status == domain.EnrollmentTransferred {
			continue
		}
		levelID, gradeID, ok := nextPlace(levels, e.LevelID, e.GradeID, sc.profile.hasGrades)
		if !ok {
			res.Finishing++
			continue
		}
		if _, err := s.repo.EnrollmentOf(ctx, sc.institution.ID, e.StudentID, toPeriodID); err == nil {
			res.Already++
			continue
		}
		st, err := s.repo.Student(ctx, sc.institution.ID, e.StudentID)
		if err != nil {
			return res, err
		}
		if st.Status != domain.StudentActive {
			res.Inactive++
			continue
		}
		sectionID := ""
		for _, x := range sections {
			if x.LevelID == levelID && x.GradeID == gradeID && x.Name == e.SectionName && room[x.ID] > 0 {
				sectionID = x.ID
				break
			}
		}
		if sectionID == "" {
			res.WithoutSection++
		} else {
			room[sectionID]--
		}
		res.Enrolled++
		if preview {
			continue
		}
		units, err := s.units(ctx, sc, levelID, gradeID, nil)
		if err != nil {
			return res, err
		}
		if _, err := s.repo.CreateEnrollment(ctx, sc.institution.ID, domain.EnrollmentInput{
			StudentID: e.StudentID, PeriodID: toPeriodID, LevelID: levelID, GradeID: gradeID, SectionID: sectionID,
			Kind: domain.EnrollContinuing, EnrolledOn: s.now(), UnitIDs: units,
		}); err != nil {
			return res, err
		}
	}
	if !preview && res.Enrolled > 0 {
		s.changed(ctx, sc, "enrollment")
	}
	return res, nil
}

// nextPlace is where a continuing student goes: the next grade, or the first grade of the next level; without
// grades (CEPRE) the same area. ok is false when the student finishes
func nextPlace(levels []domain.Level, levelID, gradeID string, hasGrades bool) (string, string, bool) {
	if !hasGrades {
		return levelID, "", true
	}
	li := slices.IndexFunc(levels, func(l domain.Level) bool { return l.ID == levelID })
	if li < 0 {
		return "", "", false
	}
	grades := levels[li].Grades
	gi := slices.IndexFunc(grades, func(g domain.Grade) bool { return g.ID == gradeID })
	if gi >= 0 && gi+1 < len(grades) {
		return levelID, grades[gi+1].ID, true
	}
	// Institutes: each program stands alone, its last semester finishes
	if levels[li].FormativeLevel != "" {
		return "", "", false
	}
	for _, next := range levels[li+1:] {
		if next.IsActive && len(next.Grades) > 0 {
			return next.ID, next.Grades[0].ID, true
		}
	}
	return "", "", false
}

// ---------- placements (EBA) ----------

func (s *PeopleService) Placements(ctx context.Context, token, slug string) ([]domain.Placement, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return nil, err
	}
	return s.repo.Placements(ctx, sc.institution.ID, "")
}

func (s *PeopleService) CreatePlacement(ctx context.Context, token, slug string, in domain.PlacementInput) (domain.Placement, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Placement{}, err
	}
	if !sc.profile.usesAttentionModes {
		return domain.Placement{}, &domain.Error{Kind: domain.KindInvalid, Code: "no_placements", Message: "La ubicación y convalidación son propias de la Educación Básica Alternativa."}
	}
	fields := map[string]string{}
	if in.Kind != "placement_test" && in.Kind != "validation" {
		fields["kind"] = "Elige si es prueba de ubicación o convalidación."
	}
	in.Reference, in.Notes = strings.TrimSpace(in.Reference), strings.TrimSpace(in.Notes)
	if utf8.RuneCountInString(in.Reference) > 120 || utf8.RuneCountInString(in.Notes) > 1000 {
		fields["reference"] = "El texto es demasiado largo."
	}
	if in.DecidedOn.IsZero() {
		in.DecidedOn = s.now()
	}
	if in.DecidedOn.After(s.now()) {
		fields["decidedOn"] = "No puede ser una fecha futura."
	}
	if level, err := s.academic.repo.Level(ctx, sc.institution.ID, in.LevelID); err != nil {
		fields["levelId"] = "Elige el ciclo."
	} else if !slices.ContainsFunc(level.Grades, func(g domain.Grade) bool { return g.ID == in.GradeID }) {
		fields["gradeId"] = "Elige un grado de " + level.Name + "."
	}
	if len(fields) > 0 {
		return domain.Placement{}, domain.Validation(fields)
	}
	p, err := s.repo.CreatePlacement(ctx, sc.institution.ID, in)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Placement{}, domain.Validation(map[string]string{"studentId": "Elige un estudiante."})
	}
	if err == nil {
		s.changed(ctx, sc, "placements")
	}
	return p, err
}

func (s *PeopleService) DeletePlacement(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if err := s.repo.DeletePlacement(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "placements")
	return nil
}
