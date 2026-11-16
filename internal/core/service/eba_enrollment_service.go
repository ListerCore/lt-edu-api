package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
)

// EBA enrollment (phase E1 of docs/ceba/PLAN.md): the ficha única, the minimum age of each program, the exemption
// from Educación Religiosa, the nómina with its SIAGIE upload state and the placement procedures

var (
	motherTongues  = map[string]bool{"castellano": true, "quechua": true, "aimara": true, "ashaninka": true, "other": true}
	internetKinds  = map[string]bool{"none": true, "mobile": true, "home": true, "public": true, "unknown": true}
	deviceKinds    = map[string]bool{"none": true, "phone": true, "tablet": true, "computer": true, "unknown": true}
	fileDocuments  = map[string]bool{"dni": true, "study_certificate": true, "birth_certificate": true, "placement_act": true, "other": true}
	siagieStates   = map[string]bool{domain.SiagiePending: true, domain.SiagieExported: true, domain.SiagieObserved: true}
	placementKinds = map[string]string{domain.PlacementTest: "Prueba de ubicación", domain.PlacementValidation: "Convalidación",
		domain.PlacementRevalidation: "Revalidación", domain.PlacementEquivalence: "Equivalencia"}
)

// ebaScope is the people scope of an EBA institution (the only kind with these procedures)
func (s *PeopleService) ebaScope(ctx context.Context, token, slug string, write bool) (academicScope, error) {
	sc, err := s.scope(ctx, token, slug, write)
	if err != nil {
		return sc, err
	}
	if !sc.profile.usesAttentionModes {
		return sc, &domain.Error{Kind: domain.KindInvalid, Code: "not_eba", Message: "Esta función es propia de la Educación Básica Alternativa."}
	}
	return sc, nil
}

// actorName is the signed-in person's name, for the records and the audit
func (s *PeopleService) actorName(ctx context.Context, token string) string {
	me, err := s.academic.auth.Me(ctx, token)
	if err != nil {
		return ""
	}
	if me.User.FullName != "" {
		return me.User.FullName
	}
	return me.User.Email
}

func (s *PeopleService) record(ctx context.Context, sc academicScope, e AuditEntry) {
	e.Module = "enrollment"
	s.academic.audit.Record(ctx, sc.institutionScope, e)
}

// ---------- ficha única ----------

func (s *PeopleService) EnrollmentFile(ctx context.Context, token, slug, studentID string) (domain.EnrollmentFile, error) {
	sc, err := s.ebaScope(ctx, token, slug, false)
	if err != nil {
		return domain.EnrollmentFile{}, err
	}
	return s.repo.EnrollmentFile(ctx, sc.institution.ID, studentID)
}

// validateFile trims and checks a ficha
func validateFile(f *domain.EnrollmentFile) error {
	fields := map[string]string{}
	f.MotherTongueOther, f.Occupation = strings.TrimSpace(f.MotherTongueOther), strings.TrimSpace(f.Occupation)
	f.Disability, f.LastStudies = strings.TrimSpace(f.Disability), strings.TrimSpace(f.LastStudies)
	if f.MotherTongue == "" {
		f.MotherTongue = "castellano"
	}
	if !motherTongues[f.MotherTongue] {
		fields["motherTongue"] = "Elige la lengua materna."
	}
	if f.MotherTongue == "other" && f.MotherTongueOther == "" {
		fields["motherTongueOther"] = "Escribe cuál es la lengua materna."
	}
	if f.MotherTongue != "other" {
		f.MotherTongueOther = ""
	}
	if f.Internet == "" {
		f.Internet = "unknown"
	}
	if f.Device == "" {
		f.Device = "unknown"
	}
	if !internetKinds[f.Internet] {
		fields["internet"] = "Elige cómo se conecta a internet."
	}
	if !deviceKinds[f.Device] {
		fields["device"] = "Elige el dispositivo que usa."
	}
	if f.PreferredMode != "" && !validModes[f.PreferredMode] {
		fields["preferredMode"] = "Elige presencial, semipresencial o a distancia."
	}
	// A distance student needs some connection: online with internet, or the itinerant variant without it
	if f.PreferredMode == "distance" && f.Internet == "none" && f.Device == "none" {
		fields["preferredMode"] = "Sin internet ni dispositivo, la forma a distancia solo puede ser itinerante: confírmalo con la dirección."
	}
	for _, d := range f.Documents {
		if !fileDocuments[d] {
			fields["documents"] = "Uno de los documentos no es válido."
		}
	}
	f.Documents = unique(f.Documents)
	if utf8.RuneCountInString(f.MotherTongueOther) > 60 || utf8.RuneCountInString(f.Occupation) > 120 || utf8.RuneCountInString(f.Disability) > 200 ||
		utf8.RuneCountInString(f.LastStudies) > 160 {
		fields["occupation"] = "Uno de los textos es demasiado largo."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

func (s *PeopleService) SaveEnrollmentFile(ctx context.Context, token, slug string, f domain.EnrollmentFile) (domain.EnrollmentFile, error) {
	sc, err := s.ebaScope(ctx, token, slug, true)
	if err != nil {
		return domain.EnrollmentFile{}, err
	}
	if err := validateFile(&f); err != nil {
		return domain.EnrollmentFile{}, err
	}
	f.UpdatedByName = s.actorName(ctx, token)
	if err := s.repo.SaveEnrollmentFile(ctx, sc.institution.ID, f); err != nil {
		return domain.EnrollmentFile{}, err
	}
	s.record(ctx, sc, AuditEntry{Action: domain.AuditUpdate, Entity: "enrollment_file", EntityID: f.StudentID, Summary: "Actualizó la ficha única de matrícula"})
	s.changed(ctx, sc, "students")
	return s.repo.EnrollmentFile(ctx, sc.institution.ID, f.StudentID)
}

// ---------- minimum age ----------

// checkAge keeps EBA enrollments at the program's minimum age on the enrollment day: 14 in PEBAJA, 15 in
// Alfabetización. Without a birth date there is nothing to check (the ficha asks for it)
func (s *PeopleService) checkAge(ctx context.Context, sc academicScope, st domain.Student, sectionID string, day time.Time) error {
	if !sc.profile.usesAttentionModes || st.BirthDate == nil {
		return nil
	}
	minAge, program := domain.MinAgePEBAJA, "PEBAJA"
	if sectionID != "" {
		sec, err := s.academic.repo.Section(ctx, sc.institution.ID, sectionID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if sec.Program == domain.EBAProgramLiteracy {
			minAge, program = domain.MinAgeLiteracy, "Alfabetización"
		}
	}
	if age := domain.AgeOn(*st.BirthDate, day); age < minAge {
		return &domain.Error{Kind: domain.KindInvalid, Code: domain.ErrUnderAge.Code,
			Message: fmt.Sprintf("%s se matricula desde los %d años; el estudiante tiene %d.", program, minAge, age)}
	}
	return nil
}

// ---------- religion exemption ----------

// SetReligionExemption grants (exempt) or withdraws the exemption from Educación Religiosa of an enrollment
func (s *PeopleService) SetReligionExemption(ctx context.Context, token, slug, enrollmentID string, exempt bool, requestedOn time.Time, reference string) error {
	sc, err := s.ebaScope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	e, err := s.repo.Enrollment(ctx, sc.institution.ID, enrollmentID)
	if err != nil {
		return err
	}
	if !exempt {
		if err := s.repo.RemoveReligionExemption(ctx, sc.institution.ID, enrollmentID); err != nil {
			return err
		}
		s.record(ctx, sc, AuditEntry{Action: domain.AuditStatus, Entity: "religion_exemption", EntityID: enrollmentID,
			Summary: "Retiró la exoneración de Educación Religiosa de " + e.StudentName})
		s.changed(ctx, sc, "enrollment")
		return nil
	}
	ok, err := s.repo.HasReligion(ctx, sc.institution.ID, enrollmentID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotExemptable
	}
	reference = strings.TrimSpace(reference)
	fields := map[string]string{}
	if requestedOn.IsZero() {
		requestedOn = s.now()
	}
	if requestedOn.After(s.now()) {
		fields["requestedOn"] = "No puede ser una fecha futura."
	}
	if utf8.RuneCountInString(reference) > 120 {
		fields["reference"] = "Hasta 120 caracteres."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	if err := s.repo.SetReligionExemption(ctx, sc.institution.ID, domain.ReligionExemption{EnrollmentID: enrollmentID, RequestedOn: requestedOn, Reference: reference,
		GrantedByName: s.actorName(ctx, token)}); err != nil {
		return err
	}
	s.record(ctx, sc, AuditEntry{Action: domain.AuditStatus, Entity: "religion_exemption", EntityID: enrollmentID,
		Summary: "Exoneró de Educación Religiosa a " + e.StudentName, Changes: map[string]any{"reference": reference}})
	s.changed(ctx, sc, "enrollment")
	return nil
}

// ---------- nómina and SIAGIE ----------

// RollView is the nómina of a period with how many enrollments are in each SIAGIE state
type RollView struct {
	Rows      []domain.RollRow
	Pending   int
	Exported  int
	Observed  int
	Exempt    int
	CanManage bool
}

func (s *PeopleService) Roll(ctx context.Context, token, slug string, f domain.RollFilter) (RollView, error) {
	sc, err := s.ebaScope(ctx, token, slug, false)
	if err != nil {
		return RollView{}, err
	}
	if f.SiagieStatus != "" && !siagieStates[f.SiagieStatus] {
		return RollView{}, domain.Validation(map[string]string{"siagieStatus": "Elige un estado de carga."})
	}
	return s.roll(ctx, sc, f)
}

func (s *PeopleService) roll(ctx context.Context, sc academicScope, f domain.RollFilter) (RollView, error) {
	if _, err := s.academic.repo.Period(ctx, sc.institution.ID, f.PeriodID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return RollView{}, domain.Validation(map[string]string{"periodId": "Elige el periodo."})
		}
		return RollView{}, err
	}
	rows, err := s.repo.Roll(ctx, sc.institution.ID, f)
	if err != nil {
		return RollView{}, err
	}
	out := RollView{Rows: rows, CanManage: peopleManagers[sc.role]}
	for _, r := range rows {
		switch r.SiagieStatus {
		case domain.SiagieExported:
			out.Exported++
		case domain.SiagieObserved:
			out.Observed++
		default:
			out.Pending++
		}
		if r.ReligionExempt {
			out.Exempt++
		}
	}
	return out, nil
}

// SetSiagie marks enrollments as pending, exported or observed (observed needs the reason the UGEL gave)
func (s *PeopleService) SetSiagie(ctx context.Context, token, slug string, enrollmentIDs []string, status, note string) (int, error) {
	sc, err := s.ebaScope(ctx, token, slug, true)
	if err != nil {
		return 0, err
	}
	note = strings.TrimSpace(note)
	switch {
	case !siagieStates[status]:
		return 0, domain.Validation(map[string]string{"status": "Elige un estado de carga."})
	case len(enrollmentIDs) == 0:
		return 0, domain.Validation(map[string]string{"enrollmentIds": "Elige al menos una matrícula."})
	case status == domain.SiagieObserved && note == "":
		return 0, domain.Validation(map[string]string{"note": "Escribe la observación del SIAGIE o la UGEL."})
	case utf8.RuneCountInString(note) > 300:
		return 0, domain.Validation(map[string]string{"note": "Hasta 300 caracteres."})
	}
	if status != domain.SiagieObserved {
		note = ""
	}
	n, err := s.repo.SetSiagie(ctx, sc.institution.ID, unique(enrollmentIDs), status, note, s.actorName(ctx, token), s.now())
	if err != nil {
		return 0, err
	}
	s.changed(ctx, sc, "enrollment")
	return n, nil
}

// ---------- placement ----------

// PlacementProposal is the cycle and grade the cuadro de equivalencias proposes for the studies a student brings
type PlacementProposal struct {
	LevelID   string
	LevelName string
	GradeID   string
	GradeName string
	// The studies already complete the EBA: there is nothing to enter
	Done bool
}

// ProposePlacement reads the cuadro de equivalencias for `passed` grades of EBR primaria or secundaria and returns
// the institution's matching cycle and grade
func (s *PeopleService) ProposePlacement(ctx context.Context, token, slug, origin string, passed int) (PlacementProposal, error) {
	sc, err := s.ebaScope(ctx, token, slug, false)
	if err != nil {
		return PlacementProposal{}, err
	}
	code, pos, done, ok := domain.EquivalentEntry(origin, passed)
	if !ok || passed < 0 || passed > 6 {
		return PlacementProposal{}, domain.Validation(map[string]string{"origin": "Elige primaria o secundaria de EBR y los grados aprobados."})
	}
	if done {
		return PlacementProposal{Done: true}, nil
	}
	levels, err := s.academic.repo.Levels(ctx, sc.institution.ID)
	if err != nil {
		return PlacementProposal{}, err
	}
	for _, l := range levels {
		if l.Code != code {
			continue
		}
		for _, g := range l.Grades {
			if g.Position == pos {
				return PlacementProposal{LevelID: l.ID, LevelName: l.Name, GradeID: g.ID, GradeName: g.Name}, nil
			}
		}
	}
	return PlacementProposal{}, &domain.Error{Kind: domain.KindNotFound, Code: "no_equivalent_grade",
		Message: "La estructura no tiene el ciclo y grado que propone el cuadro de equivalencias. Revisa los ciclos en Estructura."}
}

// validatePlacement checks the procedure's kind, texts and validated areas (of the cycle's plan in force)
func (s *PeopleService) validatePlacement(ctx context.Context, sc academicScope, in *domain.PlacementInput, fields map[string]string) error {
	if _, ok := placementKinds[in.Kind]; !ok {
		fields["kind"] = "Elige el tipo de trámite."
	}
	in.Origin, in.OriginStudies = strings.TrimSpace(in.Origin), strings.TrimSpace(in.OriginStudies)
	if (in.Kind == domain.PlacementValidation || in.Kind == domain.PlacementRevalidation) && in.OriginStudies == "" {
		fields["originStudies"] = "Indica los estudios que trae (grado y modalidad)."
	}
	if utf8.RuneCountInString(in.Origin) > 160 || utf8.RuneCountInString(in.OriginStudies) > 160 {
		fields["origin"] = "El texto es demasiado largo."
	}
	in.SubjectIDs = unique(in.SubjectIDs)
	if len(in.SubjectIDs) == 0 || fields["levelId"] != "" {
		return nil
	}
	if in.Kind == domain.PlacementTest {
		fields["subjectIds"] = "La prueba de ubicación fija el grado; las áreas se convalidan con certificados."
		return nil
	}
	plan, err := s.planSubjects(ctx, sc, in.LevelID)
	if err != nil {
		return err
	}
	for _, id := range in.SubjectIDs {
		if !slices.ContainsFunc(plan, func(x domain.Subject) bool { return x.ID == id }) {
			fields["subjectIds"] = "Una de las áreas no es del plan vigente del ciclo."
		}
	}
	return nil
}

// planSubjects are the subjects of the plan in force of a level
func (s *PeopleService) planSubjects(ctx context.Context, sc academicScope, levelID string) ([]domain.Subject, error) {
	plans, err := s.curricula.Curricula(ctx, sc.institution.ID)
	if err != nil {
		return nil, err
	}
	for _, p := range plans {
		if p.Status == domain.CurriculumActive && p.LevelID == levelID {
			full, err := s.curricula.Curriculum(ctx, sc.institution.ID, p.ID)
			if err != nil {
				return nil, err
			}
			return full.Subjects, nil
		}
	}
	return nil, nil
}
