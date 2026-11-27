package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// EventProgramsChanged: {institution, area (internships | certificates | requirements | coordinators), student}
const EventProgramsChanged = "programs.changed"

// RoleProgramCoordinator manages one or more study programs of an institute
const RoleProgramCoordinator = "program_coordinator"

var (
	programManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	// Secretaría académica issues the certificates and checks the degree requirements
	programIssuers = map[string]bool{"secretary": true}
	programReaders = map[string]bool{"teacher": true, "tutor": true, "psychologist": true}
	rucPattern     = regexp.MustCompile(`^[12][0-9]{10}$`)
)

// Degree requirements checked by hand; the others (units, placements, modular certificates) come from the record
var manualRequirements = []struct{ Code, Name string }{
	{"language", "Idioma extranjero o lengua originaria (nivel básico)"},
	{"project", "Trabajo de aplicación profesional o examen de suficiencia profesional"},
	{"no_debt", "Sin deudas con la institución"},
}

// ProgramsService runs the institutes' study programs (phase 9 of docs/plan-academico.md): each student's record by
// module, the work placements (EFSRT) with their hours and evaluation, modular certificates and the degree
type ProgramsService struct {
	repo       port.ProgramsRepository
	assessment *AssessmentService
}

func NewProgramsService(repo port.ProgramsRepository, assessment *AssessmentService) *ProgramsService {
	return &ProgramsService{repo: repo, assessment: assessment}
}

type programScope struct {
	attendanceScope
	passing int
	// Programs the person may manage (nil: every program, for managers)
	own []string
}

func (sc programScope) manager() bool {
	return programManagers[sc.role] || sc.role == RoleProgramCoordinator
}

func (sc programScope) issuer() bool { return sc.manager() || programIssuers[sc.role] }

func (sc programScope) manages(levelID string) bool {
	return programManagers[sc.role] || (sc.role == RoleProgramCoordinator && slices.Contains(sc.own, levelID)) || programIssuers[sc.role]
}

func (s *ProgramsService) scope(ctx context.Context, token, slug string) (programScope, error) {
	isc, err := resolveInstitution(ctx, s.assessment.academic.auth, s.assessment.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return programScope{}, err
	}
	if isc.institution.Kind != domain.KindInstitute {
		return programScope{}, domain.ErrNotAnInstitute
	}
	me, err := s.assessment.academic.auth.Principal(ctx, token)
	if err != nil {
		return programScope{}, err
	}
	sc := programScope{attendanceScope: attendanceScope{academicScope: academicScope{institutionScope: isc, profile: profileFor(isc.institution.Kind)}, email: me.User.Email, userName: me.User.FullName}, passing: 13}
	if !programManagers[sc.role] && !programIssuers[sc.role] && !programReaders[sc.role] && sc.role != RoleProgramCoordinator && !sc.family() {
		return sc, domain.ErrProgramsForbidden
	}
	settings, err := s.assessment.academic.settings(ctx, sc.academicScope)
	if err != nil {
		return sc, err
	}
	if settings.PassingGrade != nil {
		sc.passing = *settings.PassingGrade
	}
	if sc.role == RoleProgramCoordinator {
		programs, err := s.repo.Programs(ctx, isc.institution.ID)
		if err != nil {
			return sc, err
		}
		if st, err := s.assessment.attendance.staff.StaffByEmail(ctx, isc.institution.ID, me.User.Email); err == nil {
			for _, p := range programs {
				if p.CoordinatorID == st.ID {
					sc.own = append(sc.own, p.LevelID)
				}
			}
		}
	}
	return sc, nil
}

func (s *ProgramsService) changed(ctx context.Context, sc programScope, area, studentID string) {
	s.assessment.academic.live.Notify(ctx, EventProgramsChanged, map[string]string{"institution": sc.institution.Slug, "area": area, "student": studentID})
}

func (s *ProgramsService) name(sc programScope) string {
	if sc.userName != "" {
		return sc.userName
	}
	return sc.email
}

// requiredHours of an EFSRT: 32 hours per practice credit and 16 per theory credit
func requiredHours(u domain.UnitRef) int {
	h := u.PracticeCredits*32 + u.TheoryCredits*16
	if h == 0 {
		h = 64
	}
	return h
}

// ---------- programs ----------

type ProgramSummary struct {
	domain.ProgramInfo
	Ongoing     int
	ToEvaluate  int
	Modulars    int
	Degrees     int
	CanManage   bool
	Modules     int
	TotalCredit int
}

type ProgramsOverview struct {
	Programs  []ProgramSummary
	CanAssign bool
	Teachers  []domain.Staff
}

func (s *ProgramsService) Overview(ctx context.Context, token, slug string) (ProgramsOverview, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ProgramsOverview{}, err
	}
	if sc.family() {
		return ProgramsOverview{}, domain.ErrProgramsForbidden
	}
	programs, err := s.repo.Programs(ctx, sc.institution.ID)
	if err != nil {
		return ProgramsOverview{}, err
	}
	internships, err := s.repo.Internships(ctx, domain.InternshipFilter{InstitutionID: sc.institution.ID})
	if err != nil {
		return ProgramsOverview{}, err
	}
	certs, err := s.repo.Certificates(ctx, sc.institution.ID, "", nil, "")
	if err != nil {
		return ProgramsOverview{}, err
	}
	out := ProgramsOverview{CanAssign: programManagers[sc.role]}
	for _, p := range programs {
		sum := ProgramSummary{ProgramInfo: p, CanManage: sc.manages(p.LevelID)}
		modules, units, err := s.repo.Structure(ctx, sc.institution.ID, p.LevelID)
		if err != nil {
			return out, err
		}
		sum.Modules = len(modules)
		for _, u := range units {
			sum.TotalCredit += u.Credits()
		}
		for _, i := range internships {
			if i.LevelID != p.LevelID {
				continue
			}
			if i.Status == "ongoing" {
				sum.Ongoing++
				if i.LoggedHours >= float64(i.RequiredHours) {
					sum.ToEvaluate++
				}
			}
		}
		for _, c := range certs {
			if c.LevelID == p.LevelID && c.Status == "issued" {
				if c.Kind == "modular" {
					sum.Modulars++
				} else {
					sum.Degrees++
				}
			}
		}
		out.Programs = append(out.Programs, sum)
	}
	if out.CanAssign {
		staff, _, err := s.assessment.attendance.staff.StaffList(ctx, domain.StaffFilter{InstitutionID: sc.institution.ID, Status: "active", Limit: 300})
		if err != nil {
			return out, err
		}
		out.Teachers = staff
	}
	return out, nil
}

func (s *ProgramsService) SetCoordinator(ctx context.Context, token, slug, levelID, staffID string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	if !programManagers[sc.role] {
		return domain.ErrProgramsReadOnly
	}
	if staffID != "" {
		if _, err := s.assessment.attendance.staff.Staff(ctx, sc.institution.ID, staffID); err != nil {
			return err
		}
	}
	if err := s.repo.SetCoordinator(ctx, sc.institution.ID, levelID, staffID); err != nil {
		return err
	}
	s.changed(ctx, sc, "coordinators", "")
	return nil
}

// ---------- students and their record ----------

type StudentProgress struct {
	domain.ProgramStudent
	Approved   int
	Total      int
	Modules    int
	Certified  int
	Degree     bool
	EFSRTDone  int
	EFSRTTotal int
}

func (s *ProgramsService) Students(ctx context.Context, token, slug, levelID, query string) ([]StudentProgress, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if sc.family() {
		return nil, domain.ErrProgramsForbidden
	}
	students, err := s.repo.Students(ctx, sc.institution.ID, levelID, strings.TrimSpace(query))
	if err != nil {
		return nil, err
	}
	modules, units, err := s.repo.Structure(ctx, sc.institution.ID, levelID)
	if err != nil {
		return nil, err
	}
	certs, err := s.repo.Certificates(ctx, sc.institution.ID, "", []string{levelID}, "")
	if err != nil {
		return nil, err
	}
	out := make([]StudentProgress, 0, len(students))
	for _, st := range students {
		standings, err := s.repo.Standings(ctx, sc.institution.ID, st.ID)
		if err != nil {
			return nil, err
		}
		internships, err := s.repo.Internships(ctx, domain.InternshipFilter{InstitutionID: sc.institution.ID, StudentID: st.ID})
		if err != nil {
			return nil, err
		}
		approved := approvedSet(standings, internships)
		p := StudentProgress{ProgramStudent: st, Modules: len(modules)}
		for _, u := range units {
			p.Total += u.Credits()
			if approved[u.SubjectID] {
				p.Approved += u.Credits()
			}
			if u.Kind == "efsrt" {
				p.EFSRTTotal++
				if approved[u.SubjectID] {
					p.EFSRTDone++
				}
			}
		}
		for _, c := range certs {
			if c.StudentID == st.ID && c.Status == "issued" {
				if c.Kind == "modular" {
					p.Certified++
				} else {
					p.Degree = true
				}
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// approvedSet: subjects passed (closing or history) and EFSRT approved
func approvedSet(standings []domain.UnitStanding, internships []domain.Internship) map[string]bool {
	out := map[string]bool{}
	for _, u := range standings {
		if u.Status == "approved" {
			out[u.SubjectID] = true
		}
	}
	for _, i := range internships {
		if i.Status == "approved" {
			out[i.SubjectID] = true
		}
	}
	return out
}

type RecordUnit struct {
	domain.UnitRef
	Standing *domain.UnitStanding
	// EFSRT: its placements
	Internships []domain.Internship
	Hours       int
}

type RecordModule struct {
	Module      domain.ModuleRef
	Units       []RecordUnit
	Complete    bool
	Certificate *domain.Certificate
	Credits     int
	Approved    int
}

type RecordRequirement struct {
	Code   string
	Name   string
	Auto   bool
	Done   bool
	Note   string
	Doer   string
	DoneAt *time.Time
}

type StudentRecord struct {
	Student      domain.ProgramStudent
	Program      domain.ProgramInfo
	Modules      []RecordModule
	Requirements []RecordRequirement
	Degree       *domain.Certificate
	Eligible     bool
	Approved     int
	Total        int
	CanManage    bool
	CanIssue     bool
	Passing      int
	Certificates []domain.Certificate
}

func (s *ProgramsService) Record(ctx context.Context, token, slug, studentID string) (StudentRecord, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return StudentRecord{}, err
	}
	if sc.family() {
		mine, err := s.assessment.attendance.ownStudents(ctx, sc.attendanceScope)
		if err != nil {
			return StudentRecord{}, err
		}
		if !slices.ContainsFunc(mine, func(h domain.CardHolder) bool { return h.ID == studentID }) {
			return StudentRecord{}, domain.ErrNotYourStudent
		}
	}
	return s.record(ctx, sc, studentID)
}

func (s *ProgramsService) record(ctx context.Context, sc programScope, studentID string) (StudentRecord, error) {
	inst := sc.institution.ID
	st, err := s.repo.Student(ctx, inst, studentID)
	if err != nil {
		return StudentRecord{}, err
	}
	programs, err := s.repo.Programs(ctx, inst)
	if err != nil {
		return StudentRecord{}, err
	}
	out := StudentRecord{Student: st, Passing: sc.passing, CanManage: !sc.family() && sc.manages(st.LevelID) && !programIssuers[sc.role], CanIssue: !sc.family() && sc.issuer() && sc.manages(st.LevelID)}
	for _, p := range programs {
		if p.LevelID == st.LevelID {
			out.Program = p
		}
	}
	modules, units, err := s.repo.Structure(ctx, inst, st.LevelID)
	if err != nil {
		return out, err
	}
	standings, err := s.repo.Standings(ctx, inst, studentID)
	if err != nil {
		return out, err
	}
	internships, err := s.repo.Internships(ctx, domain.InternshipFilter{InstitutionID: inst, StudentID: studentID})
	if err != nil {
		return out, err
	}
	certs, err := s.repo.Certificates(ctx, inst, studentID, nil, "")
	if err != nil {
		return out, err
	}
	out.Certificates = certs
	// The best standing of each subject: approved over failed over ongoing
	best := map[string]domain.UnitStanding{}
	rank := map[string]int{"approved": 3, "ongoing": 2, "failed": 1}
	for _, u := range standings {
		if cur, ok := best[u.SubjectID]; !ok || rank[u.Status] >= rank[cur.Status] {
			best[u.SubjectID] = u
		}
	}
	allModules := true
	for _, m := range modules {
		rm := RecordModule{Module: m, Complete: true}
		for _, u := range units {
			if u.ModuleID != m.ID {
				continue
			}
			ru := RecordUnit{UnitRef: u}
			if b, ok := best[u.SubjectID]; ok {
				ru.Standing = &b
			}
			if u.Kind == "efsrt" {
				ru.Hours = requiredHours(u)
				for _, i := range internships {
					if i.SubjectID == u.SubjectID {
						ru.Internships = append(ru.Internships, i)
						if i.Status == "approved" {
							score := i.Score
							ru.Standing = &domain.UnitStanding{SubjectID: u.SubjectID, Status: "approved", Grade: score, PeriodName: i.Company, Source: "efsrt"}
						} else if ru.Standing == nil && i.Status == "ongoing" {
							ru.Standing = &domain.UnitStanding{SubjectID: u.SubjectID, Status: "ongoing", PeriodName: i.Company, Source: "efsrt"}
						}
					}
				}
			}
			rm.Credits += u.Credits()
			out.Total += u.Credits()
			if ru.Standing != nil && ru.Standing.Status == "approved" {
				rm.Approved += u.Credits()
				out.Approved += u.Credits()
			} else {
				rm.Complete = false
			}
			rm.Units = append(rm.Units, ru)
		}
		for _, c := range certs {
			if c.Kind == "modular" && c.ModuleID == m.ID && c.Status == "issued" {
				cc := c
				rm.Certificate = &cc
			}
		}
		if rm.Certificate == nil {
			allModules = false
		}
		out.Modules = append(out.Modules, rm)
	}
	for _, c := range certs {
		if c.Kind == "degree" && c.Status == "issued" && c.LevelID == st.LevelID {
			cc := c
			out.Degree = &cc
		}
	}
	unitsDone, efsrtDone := true, true
	for _, m := range out.Modules {
		for _, u := range m.Units {
			ok := u.Standing != nil && u.Standing.Status == "approved"
			if u.Kind == "efsrt" && !ok {
				efsrtDone = false
			} else if !ok {
				unitsDone = false
			}
		}
	}
	reqs, err := s.repo.Requirements(ctx, studentID)
	if err != nil {
		return out, err
	}
	out.Requirements = []RecordRequirement{
		{Code: "units", Name: "Todas las unidades didácticas aprobadas", Auto: true, Done: unitsDone},
		{Code: "efsrt", Name: "Experiencias formativas de todos los módulos aprobadas", Auto: true, Done: efsrtDone},
		{Code: "modular", Name: "Certificados modulares emitidos", Auto: true, Done: allModules && len(modules) > 0},
	}
	for _, m := range manualRequirements {
		r := RecordRequirement{Code: m.Code, Name: m.Name}
		for _, q := range reqs {
			if q.Code == m.Code {
				r.Done, r.Note, r.Doer, r.DoneAt = q.Done, q.Note, q.CheckedName, q.CheckedAt
			}
		}
		out.Requirements = append(out.Requirements, r)
	}
	out.Eligible = true
	for _, r := range out.Requirements {
		if !r.Done {
			out.Eligible = false
		}
	}
	return out, nil
}

// MyRecord: the student (or their family) sees their record
func (s *ProgramsService) MyRecords(ctx context.Context, token, slug string) ([]StudentRecord, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if !sc.family() {
		return nil, nil
	}
	mine, err := s.assessment.attendance.ownStudents(ctx, sc.attendanceScope)
	if err != nil {
		return nil, err
	}
	var out []StudentRecord
	for _, h := range mine {
		r, err := s.record(ctx, sc, h.ID)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ---------- internships (EFSRT) ----------

func (s *ProgramsService) Internships(ctx context.Context, token, slug, levelID, status string) ([]domain.Internship, bool, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, false, err
	}
	if sc.family() {
		return nil, false, domain.ErrProgramsForbidden
	}
	f := domain.InternshipFilter{InstitutionID: sc.institution.ID, Status: status}
	if levelID != "" {
		f.LevelIDs = []string{levelID}
	}
	list, err := s.repo.Internships(ctx, f)
	return list, sc.manager(), err
}

type InternshipDetail struct {
	Internship domain.Internship
	Logs       []domain.InternshipLog
	CanEdit    bool
	Passing    int
}

func (s *ProgramsService) Internship(ctx context.Context, token, slug, id string) (InternshipDetail, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return InternshipDetail{}, err
	}
	x, err := s.repo.Internship(ctx, sc.institution.ID, id)
	if err != nil {
		return InternshipDetail{}, err
	}
	if sc.family() {
		mine, err := s.assessment.attendance.ownStudents(ctx, sc.attendanceScope)
		if err != nil {
			return InternshipDetail{}, err
		}
		if !slices.ContainsFunc(mine, func(h domain.CardHolder) bool { return h.ID == x.StudentID }) {
			return InternshipDetail{}, domain.ErrNotYourStudent
		}
	}
	logs, err := s.repo.Logs(ctx, id)
	return InternshipDetail{Internship: x, Logs: logs, CanEdit: !sc.family() && sc.manager() && sc.manages(x.LevelID), Passing: sc.passing}, err
}

func (s *ProgramsService) editInternship(ctx context.Context, sc programScope, levelID string) error {
	if !sc.manager() {
		return domain.ErrProgramsReadOnly
	}
	if !sc.manages(levelID) {
		return domain.ErrNotYourProgram
	}
	return nil
}

func (s *ProgramsService) SaveInternship(ctx context.Context, token, slug string, x domain.Internship) (domain.Internship, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Internship{}, err
	}
	if x.ID != "" {
		cur, err := s.repo.Internship(ctx, sc.institution.ID, x.ID)
		if err != nil {
			return domain.Internship{}, err
		}
		if err := s.editInternship(ctx, sc, cur.LevelID); err != nil {
			return domain.Internship{}, err
		}
		if cur.Status != "ongoing" {
			return domain.Internship{}, domain.ErrInternshipClosed
		}
		x.StudentID, x.SubjectID = cur.StudentID, cur.SubjectID
	} else {
		st, err := s.repo.Student(ctx, sc.institution.ID, x.StudentID)
		if err != nil {
			return domain.Internship{}, err
		}
		if err := s.editInternship(ctx, sc, st.LevelID); err != nil {
			return domain.Internship{}, err
		}
		_, units, err := s.repo.Structure(ctx, sc.institution.ID, st.LevelID)
		if err != nil {
			return domain.Internship{}, err
		}
		i := slices.IndexFunc(units, func(u domain.UnitRef) bool { return u.SubjectID == x.SubjectID && u.Kind == "efsrt" })
		if i < 0 {
			return domain.Internship{}, domain.ErrNotEFSRT
		}
		existing, err := s.repo.Internships(ctx, domain.InternshipFilter{InstitutionID: sc.institution.ID, StudentID: x.StudentID})
		if err != nil {
			return domain.Internship{}, err
		}
		for _, e := range existing {
			if e.SubjectID == x.SubjectID && (e.Status == "ongoing" || e.Status == "approved") {
				return domain.Internship{}, domain.Validation(map[string]string{"subjectId": "Ya tiene esa experiencia formativa en curso o aprobada."})
			}
		}
		x.RequiredHours = requiredHours(units[i])
	}
	x.Company, x.CompanyRUC, x.Area = strings.TrimSpace(x.Company), strings.TrimSpace(x.CompanyRUC), strings.TrimSpace(x.Area)
	x.Supervisor, x.SupervisorContact = strings.TrimSpace(x.Supervisor), strings.TrimSpace(x.SupervisorContact)
	fields := map[string]string{}
	if x.Company == "" || len([]rune(x.Company)) > 160 {
		fields["company"] = "Indica la empresa o institución."
	}
	if x.CompanyRUC != "" && !rucPattern.MatchString(x.CompanyRUC) {
		fields["companyRuc"] = "El RUC tiene 11 dígitos y empieza con 1 o 2."
	}
	if x.Supervisor == "" || len([]rune(x.Supervisor)) > 160 {
		fields["supervisor"] = "Indica quién lo supervisa en la empresa."
	}
	if len([]rune(x.Area)) > 120 || len([]rune(x.SupervisorContact)) > 160 {
		fields["area"] = "El área o el contacto son demasiado largos."
	}
	if x.StartsOn.IsZero() {
		fields["startsOn"] = "Indica la fecha de inicio."
	}
	if x.EndsOn != nil && x.EndsOn.Before(x.StartsOn) {
		fields["endsOn"] = "La fecha de fin no puede ser antes del inicio."
	}
	if len(fields) > 0 {
		return domain.Internship{}, domain.Validation(fields)
	}
	out, err := s.repo.SaveInternship(ctx, sc.institution.ID, x)
	if err == nil {
		s.changed(ctx, sc, "internships", out.StudentID)
	}
	return out, err
}

func (s *ProgramsService) DeleteInternship(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	cur, err := s.repo.Internship(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if err := s.editInternship(ctx, sc, cur.LevelID); err != nil {
		return err
	}
	if cur.Status != "ongoing" {
		return domain.ErrInternshipClosed
	}
	if err := s.repo.DeleteInternship(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "internships", cur.StudentID)
	return nil
}

func (s *ProgramsService) AddLog(ctx context.Context, token, slug, id string, l domain.InternshipLog) (InternshipDetail, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return InternshipDetail{}, err
	}
	cur, err := s.repo.Internship(ctx, sc.institution.ID, id)
	if err != nil {
		return InternshipDetail{}, err
	}
	if err := s.editInternship(ctx, sc, cur.LevelID); err != nil {
		return InternshipDetail{}, err
	}
	if cur.Status != "ongoing" {
		return InternshipDetail{}, domain.ErrInternshipClosed
	}
	l.Activity = strings.TrimSpace(l.Activity)
	fields := map[string]string{}
	if l.Day.IsZero() || l.Day.Before(cur.StartsOn) || l.Day.After(time.Now().AddDate(0, 0, 1)) {
		fields["day"] = "El día va desde el inicio de la experiencia hasta hoy."
	}
	if l.Hours <= 0 || l.Hours > 12 {
		fields["hours"] = "Registra de 0,5 a 12 horas por día."
	}
	if len([]rune(l.Activity)) > 300 {
		fields["activity"] = "La actividad tiene hasta 300 caracteres."
	}
	if len(fields) > 0 {
		return InternshipDetail{}, domain.Validation(fields)
	}
	if err := s.repo.AddLog(ctx, id, l); err != nil {
		return InternshipDetail{}, err
	}
	s.changed(ctx, sc, "internships", cur.StudentID)
	return s.Internship(ctx, token, slug, id)
}

func (s *ProgramsService) DeleteLog(ctx context.Context, token, slug, id, logID string) (InternshipDetail, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return InternshipDetail{}, err
	}
	cur, err := s.repo.Internship(ctx, sc.institution.ID, id)
	if err != nil {
		return InternshipDetail{}, err
	}
	if err := s.editInternship(ctx, sc, cur.LevelID); err != nil {
		return InternshipDetail{}, err
	}
	if cur.Status != "ongoing" {
		return InternshipDetail{}, domain.ErrInternshipClosed
	}
	if err := s.repo.DeleteLog(ctx, id, logID); err != nil {
		return InternshipDetail{}, err
	}
	return s.Internship(ctx, token, slug, id)
}

// Evaluate records the supervisor's evaluation: approved when the hours are complete and the score passes
func (s *ProgramsService) Evaluate(ctx context.Context, token, slug, id string, score int, evaluation string) (InternshipDetail, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return InternshipDetail{}, err
	}
	cur, err := s.repo.Internship(ctx, sc.institution.ID, id)
	if err != nil {
		return InternshipDetail{}, err
	}
	if err := s.editInternship(ctx, sc, cur.LevelID); err != nil {
		return InternshipDetail{}, err
	}
	if cur.Status != "ongoing" {
		return InternshipDetail{}, domain.ErrInternshipClosed
	}
	evaluation = strings.TrimSpace(evaluation)
	if score < 0 || score > 20 {
		return InternshipDetail{}, domain.Validation(map[string]string{"score": "La nota va de 0 a 20."})
	}
	if evaluation == "" || len([]rune(evaluation)) > 1000 {
		return InternshipDetail{}, domain.Validation(map[string]string{"evaluation": "Resume la evaluación del supervisor (hasta 1000 caracteres)."})
	}
	status := "failed"
	if score >= sc.passing {
		if cur.LoggedHours < float64(cur.RequiredHours) {
			return InternshipDetail{}, domain.ErrHoursMissing
		}
		status = "approved"
	}
	if err := s.repo.Evaluate(ctx, sc.institution.ID, id, status, score, evaluation, s.name(sc), s.assessment.attendance.today()); err != nil {
		return InternshipDetail{}, err
	}
	s.changed(ctx, sc, "internships", cur.StudentID)
	return s.Internship(ctx, token, slug, id)
}

// ---------- certificates and degree ----------

type CertificateContent struct {
	Title       string             `json:"title"`
	Institution string             `json:"institution"`
	Program     string             `json:"program"`
	Module      string             `json:"module,omitempty"`
	Degree      string             `json:"degree,omitempty"`
	Student     string             `json:"student"`
	Document    string             `json:"document"`
	Code        string             `json:"code"`
	Credits     int                `json:"credits"`
	Units       []CertificateUnit  `json:"units"`
	Placements  []CertificatePlace `json:"placements"`
	IssuedOn    string             `json:"issuedOn"`
	Signatures  []string           `json:"signatures"`
	Legend      string             `json:"legend"`
}

type CertificateUnit struct {
	Name     string `json:"name"`
	Semester string `json:"semester"`
	Credits  int    `json:"credits"`
	Grade    string `json:"grade"`
	Period   string `json:"period"`
}

type CertificatePlace struct {
	Name    string `json:"name"`
	Company string `json:"company"`
	Hours   string `json:"hours"`
	Grade   string `json:"grade"`
}

func gradeOf(v *int) string {
	if v == nil {
		return "Apto"
	}
	return twoDigits(*v)
}

func twoDigits(v int) string { return fmt.Sprintf("%02d", v) }

func (s *ProgramsService) issueScope(ctx context.Context, token, slug, studentID string) (programScope, StudentRecord, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, StudentRecord{}, err
	}
	if !sc.issuer() {
		return sc, StudentRecord{}, domain.ErrProgramsReadOnly
	}
	rec, err := s.record(ctx, sc, studentID)
	if err != nil {
		return sc, rec, err
	}
	if !sc.manages(rec.Student.LevelID) {
		return sc, rec, domain.ErrNotYourProgram
	}
	return sc, rec, nil
}

func unitsOf(units []RecordUnit) ([]CertificateUnit, []CertificatePlace, int) {
	var us []CertificateUnit
	var ps []CertificatePlace
	credits := 0
	for _, u := range units {
		credits += u.Credits()
		if u.Kind == "efsrt" {
			p := CertificatePlace{Name: u.Name, Hours: twoOrMore(u.Hours) + " h"}
			for _, i := range u.Internships {
				if i.Status == "approved" {
					p.Company, p.Grade = i.Company, gradeOf(i.Score)
				}
			}
			if p.Company == "" && u.Standing != nil {
				p.Company, p.Grade = "Convalidada", gradeOf(u.Standing.Grade)
			}
			ps = append(ps, p)
			continue
		}
		cu := CertificateUnit{Name: u.Name, Semester: u.Semester, Credits: u.Credits()}
		if u.Standing != nil {
			cu.Grade, cu.Period = gradeOf(u.Standing.Grade), u.Standing.PeriodName
		}
		us = append(us, cu)
	}
	return us, ps, credits
}

func twoOrMore(v int) string { return strconv.Itoa(v) }

func (s *ProgramsService) IssueModular(ctx context.Context, token, slug, studentID, moduleID string) (domain.Certificate, error) {
	sc, rec, err := s.issueScope(ctx, token, slug, studentID)
	if err != nil {
		return domain.Certificate{}, err
	}
	return s.issueModular(ctx, sc, rec, moduleID)
}

func (s *ProgramsService) issueModular(ctx context.Context, sc programScope, rec StudentRecord, moduleID string) (domain.Certificate, error) {
	studentID := rec.Student.ID
	i := slices.IndexFunc(rec.Modules, func(m RecordModule) bool { return m.Module.ID == moduleID })
	if i < 0 {
		return domain.Certificate{}, domain.ErrNotFound
	}
	m := rec.Modules[i]
	if m.Certificate != nil {
		return domain.Certificate{}, domain.ErrAlreadyIssued
	}
	if !m.Complete {
		return domain.Certificate{}, domain.ErrModuleIncomplete
	}
	units, places, credits := unitsOf(m.Units)
	today := s.assessment.attendance.today()
	content := CertificateContent{Title: "Certificado modular", Institution: sc.institution.Name, Program: rec.Program.Name, Module: m.Module.Name,
		Student: displayName(rec.Student.Name), Document: rec.Student.Document, Code: rec.Student.Code, Credits: credits, Units: units, Placements: places,
		IssuedOn: today.Format(time.DateOnly), Signatures: []string{"Secretaría académica", "Dirección general"},
		Legend: "Certifica la competencia del módulo formativo, conforme a la Ley N.° 30512 y sus lineamientos académicos."}
	body, err := json.Marshal(content)
	if err != nil {
		return domain.Certificate{}, err
	}
	out, err := s.repo.IssueCertificate(ctx, sc.institution.ID, domain.Certificate{StudentID: studentID, LevelID: rec.Student.LevelID, Kind: "modular", ModuleID: moduleID, Content: body, IssuedOn: today, IssuedName: s.name(sc)})
	if err == nil {
		s.changed(ctx, sc, "certificates", studentID)
	}
	return out, err
}

func (s *ProgramsService) IssueDegree(ctx context.Context, token, slug, studentID string) (domain.Certificate, error) {
	sc, rec, err := s.issueScope(ctx, token, slug, studentID)
	if err != nil {
		return domain.Certificate{}, err
	}
	return s.issueDegree(ctx, sc, rec)
}

func (s *ProgramsService) issueDegree(ctx context.Context, sc programScope, rec StudentRecord) (domain.Certificate, error) {
	studentID := rec.Student.ID
	if rec.Degree != nil {
		return domain.Certificate{}, domain.ErrAlreadyIssued
	}
	if !rec.Eligible {
		return domain.Certificate{}, domain.ErrDegreeRequirements
	}
	var units []CertificateUnit
	var places []CertificatePlace
	credits := 0
	for _, m := range rec.Modules {
		u, p, c := unitsOf(m.Units)
		units, places, credits = append(units, u...), append(places, p...), credits+c
	}
	today := s.assessment.attendance.today()
	content := CertificateContent{Title: "Título", Institution: sc.institution.Name, Program: rec.Program.Name, Degree: "Profesional Técnico en " + rec.Program.Name,
		Student: displayName(rec.Student.Name), Document: rec.Student.Document, Code: rec.Student.Code, Credits: credits, Units: units, Placements: places,
		IssuedOn: today.Format(time.DateOnly), Signatures: []string{"Secretaría académica", "Dirección general"},
		Legend: "A nombre de la Nación. Otorgado conforme a la Ley N.° 30512, Ley de Institutos y Escuelas de Educación Superior."}
	body, err := json.Marshal(content)
	if err != nil {
		return domain.Certificate{}, err
	}
	out, err := s.repo.IssueCertificate(ctx, sc.institution.ID, domain.Certificate{StudentID: studentID, LevelID: rec.Student.LevelID, Kind: "degree", Content: body, IssuedOn: today, IssuedName: s.name(sc)})
	if err == nil {
		s.changed(ctx, sc, "certificates", studentID)
	}
	return out, err
}

// displayName turns "Apellidos, Nombres" into "Nombres Apellidos" for documents
func displayName(name string) string {
	parts := strings.SplitN(name, ", ", 2)
	if len(parts) == 2 {
		return parts[1] + " " + parts[0]
	}
	return name
}

func (s *ProgramsService) Annul(ctx context.Context, token, slug, id, reason string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	if !programManagers[sc.role] {
		return domain.ErrProgramsReadOnly
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 300 {
		return domain.Validation(map[string]string{"reason": "Explica por qué se anula (hasta 300 caracteres)."})
	}
	c, err := s.repo.Certificate(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if err := s.repo.AnnulCertificate(ctx, sc.institution.ID, id, reason); err != nil {
		return err
	}
	s.changed(ctx, sc, "certificates", c.StudentID)
	return nil
}

func (s *ProgramsService) Certificates(ctx context.Context, token, slug, levelID, kind string) ([]domain.Certificate, bool, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, false, err
	}
	if sc.family() {
		return nil, false, domain.ErrProgramsForbidden
	}
	var levels []string
	if levelID != "" {
		levels = []string{levelID}
	}
	list, err := s.repo.Certificates(ctx, sc.institution.ID, "", levels, kind)
	return list, programManagers[sc.role], err
}

func (s *ProgramsService) Certificate(ctx context.Context, token, slug, id string) (domain.Certificate, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Certificate{}, err
	}
	c, err := s.repo.Certificate(ctx, sc.institution.ID, id)
	if err != nil {
		return c, err
	}
	if sc.family() {
		mine, err := s.assessment.attendance.ownStudents(ctx, sc.attendanceScope)
		if err != nil {
			return c, err
		}
		if !slices.ContainsFunc(mine, func(h domain.CardHolder) bool { return h.ID == c.StudentID }) {
			return domain.Certificate{}, domain.ErrNotYourStudent
		}
	}
	return c, nil
}

func (s *ProgramsService) SetRequirement(ctx context.Context, token, slug, studentID, code string, done bool, note string) (StudentRecord, error) {
	sc, rec, err := s.issueScope(ctx, token, slug, studentID)
	if err != nil {
		return StudentRecord{}, err
	}
	if !slices.ContainsFunc(manualRequirements, func(m struct{ Code, Name string }) bool { return m.Code == code }) {
		return StudentRecord{}, domain.Validation(map[string]string{"code": "Ese requisito se cumple solo, con el expediente."})
	}
	if rec.Degree != nil {
		return StudentRecord{}, domain.ErrAlreadyIssued
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 300 {
		return StudentRecord{}, domain.Validation(map[string]string{"note": "La nota tiene hasta 300 caracteres."})
	}
	if err := s.repo.SetRequirement(ctx, studentID, domain.Requirement{Code: code, Done: done, Note: note, CheckedName: s.name(sc)}); err != nil {
		return StudentRecord{}, err
	}
	s.changed(ctx, sc, "requirements", studentID)
	return s.record(ctx, sc, studentID)
}
