package service

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// EventAssessmentChanged: {institution, area (marks | evaluations | tutor | submissions), section}
const EventAssessmentChanged = "assessment.changed"

var (
	gradeManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	// Read the progress and every report card, without grading
	gradeReaders = map[string]bool{"secretary": true, "psychologist": true, "program_coordinator": true}
	gradeStaff   = map[string]bool{"teacher": true, "tutor": true}
)

// AssessmentService runs the grading of each term (phase 6 of docs/plan-academico.md): levels per competency with
// descriptive conclusions (Educación Básica), evaluations 0–20 (IEST, CEPRE), the tutor's sheet (transversal
// competencies, conduct and comments), the teachers' "sent" and the report cards for staff and families
type AssessmentService struct {
	repo       port.AssessmentRepository
	teaching   port.TeachingRepository
	attendance *AttendanceService
	academic   *AcademicService
}

func NewAssessmentService(repo port.AssessmentRepository, teaching port.TeachingRepository, attendance *AttendanceService, academic *AcademicService) *AssessmentService {
	return &AssessmentService{repo: repo, teaching: teaching, attendance: attendance, academic: academic}
}

type gradeScope struct {
	attendanceScope
	settings domain.AcademicSettings
}

func (sc gradeScope) manager() bool  { return gradeManagers[sc.role] }
func (sc gradeScope) numeric() bool  { return sc.settings.GradingScale != domain.ScaleLiteral }
func (sc gradeScope) readsAll() bool { return sc.manager() || gradeReaders[sc.role] }

func (s *AssessmentService) scope(ctx context.Context, token, slug string) (gradeScope, error) {
	asc, err := s.attendance.scope(ctx, token, slug)
	if errors.Is(err, domain.ErrAttendanceForbidden) {
		// Attendance and grades share the people they serve, except the office roles of each one
		isc, e := resolveInstitution(ctx, s.academic.auth, s.academic.repo.InstitutionBySlug, token, slug)
		if e != nil {
			return gradeScope{}, e
		}
		if !gradeReaders[isc.role] {
			return gradeScope{}, domain.ErrGradesForbidden
		}
		me, e := s.academic.auth.Me(ctx, token)
		if e != nil {
			return gradeScope{}, e
		}
		asc = attendanceScope{academicScope: academicScope{institutionScope: isc, profile: profileFor(isc.institution.Kind)}, email: me.User.Email, userName: me.User.FullName}
	} else if err != nil {
		return gradeScope{}, err
	}
	if !gradeManagers[asc.role] && !gradeReaders[asc.role] && !gradeStaff[asc.role] && !asc.family() {
		return gradeScope{}, domain.ErrGradesForbidden
	}
	settings, err := s.academic.settings(ctx, asc.academicScope)
	if err != nil {
		return gradeScope{}, err
	}
	return gradeScope{attendanceScope: asc, settings: settings}, nil
}

func (s *AssessmentService) changed(ctx context.Context, sc gradeScope, area, sectionID string) {
	s.academic.live.Notify(ctx, EventAssessmentChanged, map[string]string{"institution": sc.institution.Slug, "area": area, "section": sectionID})
}

func (s *AssessmentService) userName(sc gradeScope) string {
	if sc.userName != "" {
		return sc.userName
	}
	return sc.email
}

// ---------- periods and terms ----------

// termOf finds the term in the period ("" when the period has no terms) and whether grading is locked
func termOf(p domain.Period, termID string) (domain.Term, bool, error) {
	if len(p.Terms) == 0 {
		if termID != "" {
			return domain.Term{}, false, domain.ErrNotFound
		}
		return domain.Term{Name: p.Name, StartsOn: p.StartsOn, EndsOn: p.EndsOn, Status: domain.TermOpen}, p.Status == domain.PeriodClosed, nil
	}
	for _, t := range p.Terms {
		if t.ID == termID {
			return t, t.Status == domain.TermClosed || p.Status == domain.PeriodClosed, nil
		}
	}
	return domain.Term{}, false, domain.ErrNotFound
}

// defaultTerm is the open term running today, else the last open one, else the last
func defaultTerm(p domain.Period, today time.Time) string {
	if len(p.Terms) == 0 {
		return ""
	}
	for _, t := range p.Terms {
		if t.Status == domain.TermOpen && !today.Before(t.StartsOn) && !today.After(t.EndsOn) {
			return t.ID
		}
	}
	for i := len(p.Terms) - 1; i >= 0; i-- {
		if p.Terms[i].Status == domain.TermOpen {
			return p.Terms[i].ID
		}
	}
	return p.Terms[len(p.Terms)-1].ID
}

func (s *AssessmentService) period(ctx context.Context, sc gradeScope, periodID string) (domain.Period, []domain.Period, error) {
	periods, err := s.academic.repo.Periods(ctx, sc.institution.ID)
	if err != nil {
		return domain.Period{}, nil, err
	}
	var open []domain.Period
	for _, p := range periods {
		if p.Status != domain.PeriodPlanning {
			open = append(open, p)
		}
	}
	today := s.attendance.today()
	pick := -1
	for i, p := range open {
		if p.ID == periodID {
			pick = i
		}
	}
	if pick < 0 && periodID != "" {
		return domain.Period{}, open, domain.ErrNotFound
	}
	if pick < 0 {
		for i, p := range open {
			if p.Status == domain.PeriodActive && !today.Before(p.StartsOn) && !today.After(p.EndsOn) {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		for i, p := range open {
			if p.Status == domain.PeriodActive {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		return domain.Period{}, open, nil
	}
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, open[pick].ID)
	return p, open, err
}

// ---------- gradebook ----------

type TutorProgress struct {
	SectionID    string
	SectionLabel string
	TutorName    string
	Students     int
	Expected     int
	Filled       int
	Submitted    *domain.Submission
}

type Gradebook struct {
	Periods   []domain.Period
	Period    domain.Period
	TermID    string
	Locked    bool
	Scale     string
	Passing   *int
	Conduct   bool
	Sheets    []domain.SheetProgress
	Tutor     []TutorProgress
	CanManage bool
	// Teachers see their own sheets only
	OwnOnly bool
}

func (s *AssessmentService) Gradebook(ctx context.Context, token, slug, periodID, termID string) (Gradebook, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return Gradebook{}, err
	}
	if sc.family() {
		return Gradebook{}, domain.ErrGradesForbidden
	}
	p, periods, err := s.period(ctx, sc, periodID)
	if err != nil {
		return Gradebook{}, err
	}
	out := Gradebook{Periods: periods, Period: p, Scale: sc.settings.GradingScale, Passing: sc.settings.PassingGrade, Conduct: sc.settings.ConductGrade && !sc.numeric(),
		CanManage: sc.manager(), OwnOnly: !sc.readsAll()}
	if p.ID == "" {
		return out, nil
	}
	if termID == "" {
		termID = defaultTerm(p, s.attendance.today())
	}
	if _, out.Locked, err = termOf(p, termID); err != nil {
		return out, err
	}
	out.TermID = termID
	sheets, err := s.repo.Progress(ctx, sc.institution.ID, p.ID, termID, sc.numeric())
	if err != nil {
		return out, err
	}
	subs, err := s.repo.Submissions(ctx, sc.institution.ID, p.ID, termID)
	if err != nil {
		return out, err
	}
	sent := map[string]domain.Submission{}
	for _, x := range subs {
		sent[x.SectionID+"|"+x.AssignmentID] = x
	}
	for _, sh := range sheets {
		if !sc.numeric() && sh.Expected == 0 && sh.Students > 0 {
			// Areas without competencies (Tutoría) are not graded
			continue
		}
		if out.OwnOnly && (sc.staffID == "" || sh.StaffID != sc.staffID) {
			continue
		}
		if x, ok := sent[sh.SectionID+"|"+sh.AssignmentID]; ok {
			sh.Submitted = &x
		}
		out.Sheets = append(out.Sheets, sh)
	}
	if !sc.numeric() {
		sections, err := s.academic.repo.Sections(ctx, sc.institution.ID, p.ID)
		if err != nil {
			return out, err
		}
		for _, sec := range sections {
			if out.OwnOnly && !slices.Contains(sc.tutoring, sec.ID) {
				continue
			}
			tp, err := s.tutorProgress(ctx, sc, sec.ID, termID)
			if err != nil {
				return out, err
			}
			if x, ok := sent[sec.ID+"|"]; ok {
				tp.Submitted = &x
			}
			out.Tutor = append(out.Tutor, tp)
		}
	}
	return out, nil
}

func (s *AssessmentService) tutorProgress(ctx context.Context, sc gradeScope, sectionID, termID string) (TutorProgress, error) {
	meta, err := s.repo.Section(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return TutorProgress{}, err
	}
	students, err := s.repo.Students(ctx, sc.institution.ID, sectionID, "")
	if err != nil {
		return TutorProgress{}, err
	}
	comps, err := s.repo.Transversal(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return TutorProgress{}, err
	}
	marks, err := s.repo.CompetencyMarks(ctx, sc.institution.ID, sectionID, termID, false)
	if err != nil {
		return TutorProgress{}, err
	}
	ids := map[string]bool{}
	for _, c := range comps {
		ids[c.ID] = true
	}
	enrolled := map[string]bool{}
	for _, st := range students {
		enrolled[st.ID] = true
	}
	tp := TutorProgress{SectionID: sectionID, SectionLabel: meta.Label + " · " + meta.LevelName, TutorName: meta.TutorName, Students: len(students), Expected: len(students) * len(comps)}
	for _, m := range marks {
		if ids[m.CompetencyID] && m.Level != "" && enrolled[m.StudentID] {
			tp.Filled++
		}
	}
	if sc.settings.ConductGrade {
		tp.Expected += len(students)
		conduct, err := s.repo.Conduct(ctx, sc.institution.ID, sectionID, termID, false)
		if err != nil {
			return tp, err
		}
		for _, c := range conduct {
			if enrolled[c.StudentID] {
				tp.Filled++
			}
		}
	}
	return tp, nil
}

// ---------- one sheet ----------

type Sheet struct {
	Kind         string
	Section      domain.SectionMeta
	Assignment   domain.Assignment
	Term         domain.Term
	TermID       string
	Locked       bool
	CanEdit      bool
	Passing      *int
	Conduct      bool
	Students     []domain.StudentRef
	Competencies []domain.CompetencyRef
	Marks        []domain.CompetencyMark
	Evaluations  []domain.Evaluation
	Scores       []domain.Score
	ConductMarks []domain.ConductMark
	Comments     []domain.TutorComment
	Submitted    *domain.Submission
}

// sheetFor checks who may see (and edit) an area's sheet: coordinación, its teacher, and the section's tutor (read)
func (s *AssessmentService) sheetFor(ctx context.Context, sc gradeScope, assignmentID, termID string) (Sheet, domain.Period, error) {
	if sc.family() {
		return Sheet{}, domain.Period{}, domain.ErrGradesForbidden
	}
	a, err := s.teaching.Assignment(ctx, sc.institution.ID, assignmentID)
	if err != nil {
		return Sheet{}, domain.Period{}, err
	}
	own := sc.staffID != "" && sc.staffID == a.StaffID
	if !sc.readsAll() && !own && !slices.Contains(sc.tutoring, a.SectionID) {
		return Sheet{}, domain.Period{}, domain.ErrNotYourSheet
	}
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, a.PeriodID)
	if err != nil {
		return Sheet{}, p, err
	}
	term, locked, err := termOf(p, termID)
	if err != nil {
		return Sheet{}, p, err
	}
	meta, err := s.repo.Section(ctx, sc.institution.ID, a.SectionID)
	if err != nil {
		return Sheet{}, p, err
	}
	kind := domain.SheetLiteral
	if sc.numeric() {
		kind = domain.SheetNumeric
	}
	return Sheet{Kind: kind, Section: meta, Assignment: a, Term: term, TermID: termID, Locked: locked, CanEdit: !locked && (sc.manager() || own), Passing: sc.settings.PassingGrade}, p, nil
}

func (s *AssessmentService) fillSheet(ctx context.Context, sc gradeScope, sh *Sheet) error {
	var err error
	if sh.Students, err = s.repo.Students(ctx, sc.institution.ID, sh.Section.ID, sh.Assignment.SubjectID); err != nil {
		return err
	}
	if sh.Kind == domain.SheetLiteral {
		if sh.Competencies, err = s.repo.Competencies(ctx, sc.institution.ID, sh.Assignment.SubjectID); err != nil {
			return err
		}
		all, err := s.repo.CompetencyMarks(ctx, sc.institution.ID, sh.Section.ID, sh.TermID, false)
		if err != nil {
			return err
		}
		ids := map[string]bool{}
		for _, c := range sh.Competencies {
			ids[c.ID] = true
		}
		for _, m := range all {
			if ids[m.CompetencyID] {
				sh.Marks = append(sh.Marks, m)
			}
		}
	} else {
		if sh.Evaluations, err = s.repo.Evaluations(ctx, sc.institution.ID, sh.Assignment.ID, sh.TermID); err != nil {
			return err
		}
		ids := make([]string, len(sh.Evaluations))
		for i, e := range sh.Evaluations {
			ids[i] = e.ID
		}
		if sh.Scores, err = s.repo.Scores(ctx, ids); err != nil {
			return err
		}
	}
	subs, err := s.repo.Submissions(ctx, sc.institution.ID, sh.Section.PeriodID, sh.TermID)
	if err != nil {
		return err
	}
	for _, x := range subs {
		if x.SectionID == sh.Section.ID && x.AssignmentID == sh.Assignment.ID {
			sh.Submitted = &x
		}
	}
	return nil
}

func (s *AssessmentService) Sheet(ctx context.Context, token, slug, assignmentID, termID string) (Sheet, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return Sheet{}, err
	}
	sh, _, err := s.sheetFor(ctx, sc, assignmentID, termID)
	if err != nil {
		return Sheet{}, err
	}
	return sh, s.fillSheet(ctx, sc, &sh)
}

func (s *AssessmentService) editable(ctx context.Context, token, slug, assignmentID, termID string) (gradeScope, Sheet, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, Sheet{}, err
	}
	sh, _, err := s.sheetFor(ctx, sc, assignmentID, termID)
	if err != nil {
		return sc, sh, err
	}
	if sh.Locked {
		return sc, sh, domain.ErrTermClosed
	}
	if !sh.CanEdit {
		return sc, sh, domain.ErrNotYourSheet
	}
	return sc, sh, nil
}

// SaveLevels stores levels and conclusions of an area. A C needs its conclusion (RVM 094-2020-MINEDU)
func (s *AssessmentService) SaveLevels(ctx context.Context, token, slug, assignmentID, termID string, marks []domain.CompetencyMark) (Sheet, error) {
	sc, sh, err := s.editable(ctx, token, slug, assignmentID, termID)
	if err != nil {
		return Sheet{}, err
	}
	if sh.Kind != domain.SheetLiteral {
		return Sheet{}, domain.ErrWrongSheetKind
	}
	comps, err := s.repo.Competencies(ctx, sc.institution.ID, sh.Assignment.SubjectID)
	if err != nil {
		return Sheet{}, err
	}
	students, err := s.repo.Students(ctx, sc.institution.ID, sh.Section.ID, sh.Assignment.SubjectID)
	if err != nil {
		return Sheet{}, err
	}
	clean, err := validLevels(marks, comps, students)
	if err != nil {
		return Sheet{}, err
	}
	if err := s.repo.SaveCompetencyMarks(ctx, sc.institution.ID, sh.Section.ID, termID, clean, sc.userID); err != nil {
		return Sheet{}, err
	}
	s.changed(ctx, sc, "marks", sh.Section.ID)
	return s.Sheet(ctx, token, slug, assignmentID, termID)
}

func validLevels(marks []domain.CompetencyMark, comps []domain.CompetencyRef, students []domain.StudentRef) ([]domain.CompetencyMark, error) {
	compIDs := map[string]bool{}
	for _, c := range comps {
		compIDs[c.ID] = true
	}
	stIDs := map[string]bool{}
	for _, st := range students {
		stIDs[st.ID] = true
	}
	fields := map[string]string{}
	clean := make([]domain.CompetencyMark, 0, len(marks))
	for _, m := range marks {
		m.Conclusion = strings.TrimSpace(m.Conclusion)
		switch {
		case !compIDs[m.CompetencyID] || !stIDs[m.StudentID]:
			fields["marks"] = "Hay estudiantes o competencias que no son de este registro."
		case m.Level != "" && !slices.Contains(domain.Levels, m.Level):
			fields["marks"] = "El nivel de logro es AD, A, B o C."
		case m.Level == "C" && m.Conclusion == "":
			fields[m.StudentID+":"+m.CompetencyID] = "Con C, escribe la conclusión descriptiva: qué le falta lograr y cómo se le apoyará."
		case len([]rune(m.Conclusion)) > 1000:
			fields[m.StudentID+":"+m.CompetencyID] = "La conclusión tiene hasta 1000 caracteres."
		}
		clean = append(clean, m)
	}
	if len(fields) > 0 {
		return nil, domain.Validation(fields)
	}
	return clean, nil
}

type EvaluationInput struct {
	ID     string
	Name   string
	Weight int
	HeldOn *time.Time
}

func (s *AssessmentService) SaveEvaluation(ctx context.Context, token, slug, assignmentID, termID string, in EvaluationInput) (Sheet, error) {
	sc, sh, err := s.editable(ctx, token, slug, assignmentID, termID)
	if err != nil {
		return Sheet{}, err
	}
	if sh.Kind != domain.SheetNumeric {
		return Sheet{}, domain.ErrWrongSheetKind
	}
	name := strings.TrimSpace(in.Name)
	fields := map[string]string{}
	if name == "" {
		fields["name"] = "Ponle un nombre: práctica 1, examen parcial, indicador 2…"
	} else if len([]rune(name)) > 120 {
		fields["name"] = "El nombre tiene hasta 120 caracteres."
	}
	if in.Weight < 1 || in.Weight > 100 {
		fields["weight"] = "El peso va de 1 a 100."
	}
	if len(fields) > 0 {
		return Sheet{}, domain.Validation(fields)
	}
	if in.ID != "" {
		e, err := s.repo.Evaluation(ctx, sc.institution.ID, in.ID)
		if err != nil {
			return Sheet{}, err
		}
		if e.AssignmentID != assignmentID || e.TermID != termID {
			return Sheet{}, domain.ErrNotFound
		}
	}
	if _, err := s.repo.SaveEvaluation(ctx, sc.institution.ID, domain.Evaluation{ID: in.ID, AssignmentID: assignmentID, TermID: termID, Name: name, Weight: in.Weight, HeldOn: in.HeldOn}); err != nil {
		return Sheet{}, err
	}
	s.changed(ctx, sc, "evaluations", sh.Section.ID)
	return s.Sheet(ctx, token, slug, assignmentID, termID)
}

func (s *AssessmentService) DeleteEvaluation(ctx context.Context, token, slug, assignmentID, termID, id string) (Sheet, error) {
	sc, sh, err := s.editable(ctx, token, slug, assignmentID, termID)
	if err != nil {
		return Sheet{}, err
	}
	e, err := s.repo.Evaluation(ctx, sc.institution.ID, id)
	if err != nil {
		return Sheet{}, err
	}
	if e.AssignmentID != assignmentID {
		return Sheet{}, domain.ErrNotFound
	}
	if err := s.repo.DeleteEvaluation(ctx, sc.institution.ID, id); err != nil {
		return Sheet{}, err
	}
	s.changed(ctx, sc, "evaluations", sh.Section.ID)
	return s.Sheet(ctx, token, slug, assignmentID, termID)
}

// SaveScores stores the scores 0–20 of one evaluation; nil clears a score
func (s *AssessmentService) SaveScores(ctx context.Context, token, slug, assignmentID, termID, evaluationID string, scores map[string]*int) (Sheet, error) {
	sc, sh, err := s.editable(ctx, token, slug, assignmentID, termID)
	if err != nil {
		return Sheet{}, err
	}
	e, err := s.repo.Evaluation(ctx, sc.institution.ID, evaluationID)
	if err != nil {
		return Sheet{}, err
	}
	if e.AssignmentID != assignmentID || e.TermID != termID {
		return Sheet{}, domain.ErrNotFound
	}
	students, err := s.repo.Students(ctx, sc.institution.ID, sh.Section.ID, sh.Assignment.SubjectID)
	if err != nil {
		return Sheet{}, err
	}
	ids := map[string]bool{}
	for _, st := range students {
		ids[st.ID] = true
	}
	for id, v := range scores {
		if !ids[id] {
			return Sheet{}, domain.Validation(map[string]string{"scores": "Hay estudiantes que no llevan este curso."})
		}
		if v != nil && (*v < 0 || *v > 20) {
			return Sheet{}, domain.Validation(map[string]string{"scores": "Las notas van de 0 a 20."})
		}
	}
	if err := s.repo.SaveScores(ctx, evaluationID, scores); err != nil {
		return Sheet{}, err
	}
	s.changed(ctx, sc, "marks", sh.Section.ID)
	return s.Sheet(ctx, token, slug, assignmentID, termID)
}

// ---------- the tutor's sheet ----------

func (s *AssessmentService) tutorSheetFor(ctx context.Context, sc gradeScope, sectionID, termID string) (Sheet, error) {
	if sc.family() || sc.numeric() {
		return Sheet{}, domain.ErrGradesForbidden
	}
	if !sc.readsAll() && !slices.Contains(sc.tutoring, sectionID) {
		return Sheet{}, domain.ErrNotYourSheet
	}
	meta, err := s.repo.Section(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return Sheet{}, err
	}
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, meta.PeriodID)
	if err != nil {
		return Sheet{}, err
	}
	term, locked, err := termOf(p, termID)
	if err != nil {
		return Sheet{}, err
	}
	return Sheet{Kind: domain.SheetTutor, Section: meta, Term: term, TermID: termID, Locked: locked, Conduct: sc.settings.ConductGrade,
		CanEdit: !locked && (sc.manager() || slices.Contains(sc.tutoring, sectionID))}, nil
}

func (s *AssessmentService) TutorSheet(ctx context.Context, token, slug, sectionID, termID string) (Sheet, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return Sheet{}, err
	}
	sh, err := s.tutorSheetFor(ctx, sc, sectionID, termID)
	if err != nil {
		return Sheet{}, err
	}
	return sh, s.fillTutor(ctx, sc, &sh)
}

func (s *AssessmentService) fillTutor(ctx context.Context, sc gradeScope, sh *Sheet) error {
	var err error
	id := sh.Section.ID
	if sh.Students, err = s.repo.Students(ctx, sc.institution.ID, id, ""); err != nil {
		return err
	}
	if sh.Competencies, err = s.repo.Transversal(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	all, err := s.repo.CompetencyMarks(ctx, sc.institution.ID, id, sh.TermID, false)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, c := range sh.Competencies {
		ids[c.ID] = true
	}
	for _, m := range all {
		if ids[m.CompetencyID] {
			sh.Marks = append(sh.Marks, m)
		}
	}
	if sh.Conduct {
		if sh.ConductMarks, err = s.repo.Conduct(ctx, sc.institution.ID, id, sh.TermID, false); err != nil {
			return err
		}
	}
	if sh.Comments, err = s.repo.Comments(ctx, id, sh.TermID); err != nil {
		return err
	}
	subs, err := s.repo.Submissions(ctx, sc.institution.ID, sh.Section.PeriodID, sh.TermID)
	if err != nil {
		return err
	}
	for _, x := range subs {
		if x.SectionID == id && x.AssignmentID == "" {
			sh.Submitted = &x
		}
	}
	return nil
}

type TutorInput struct {
	Marks    []domain.CompetencyMark
	Conduct  map[string]*int
	Notes    map[string]string
	Comments map[string]string
}

func (s *AssessmentService) SaveTutorSheet(ctx context.Context, token, slug, sectionID, termID string, in TutorInput) (Sheet, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return Sheet{}, err
	}
	sh, err := s.tutorSheetFor(ctx, sc, sectionID, termID)
	if err != nil {
		return Sheet{}, err
	}
	if sh.Locked {
		return Sheet{}, domain.ErrTermClosed
	}
	if !sh.CanEdit {
		return Sheet{}, domain.ErrNotYourSheet
	}
	comps, err := s.repo.Transversal(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return Sheet{}, err
	}
	students, err := s.repo.Students(ctx, sc.institution.ID, sectionID, "")
	if err != nil {
		return Sheet{}, err
	}
	clean, err := validLevels(in.Marks, comps, students)
	if err != nil {
		return Sheet{}, err
	}
	ids := map[string]bool{}
	for _, st := range students {
		ids[st.ID] = true
	}
	if len(in.Conduct) > 0 && !sh.Conduct {
		return Sheet{}, domain.ErrNoConduct
	}
	for id, v := range in.Conduct {
		if !ids[id] || v != nil && (*v < 0 || *v > 20) {
			return Sheet{}, domain.Validation(map[string]string{"conduct": "La conducta va de 0 a 20."})
		}
	}
	comments := map[string]string{}
	for id, text := range in.Comments {
		text = strings.TrimSpace(text)
		if !ids[id] || len([]rune(text)) > 1000 {
			return Sheet{}, domain.Validation(map[string]string{"comments": "El comentario tiene hasta 1000 caracteres."})
		}
		comments[id] = text
	}
	notes := map[string]string{}
	for id, n := range in.Notes {
		notes[id] = strings.TrimSpace(n)
		if len([]rune(notes[id])) > 500 {
			return Sheet{}, domain.Validation(map[string]string{"conduct": "La observación de conducta tiene hasta 500 caracteres."})
		}
	}
	if len(clean) > 0 {
		if err := s.repo.SaveCompetencyMarks(ctx, sc.institution.ID, sectionID, termID, clean, sc.userID); err != nil {
			return Sheet{}, err
		}
	}
	if len(in.Conduct) > 0 {
		if err := s.repo.SaveConduct(ctx, sc.institution.ID, sectionID, termID, in.Conduct, notes); err != nil {
			return Sheet{}, err
		}
	}
	if len(comments) > 0 {
		if err := s.repo.SaveComments(ctx, sectionID, termID, comments); err != nil {
			return Sheet{}, err
		}
	}
	s.changed(ctx, sc, "tutor", sectionID)
	return s.TutorSheet(ctx, token, slug, sectionID, termID)
}

// Submit marks a sheet as sent to coordinación (or takes it back); assignmentID "" is the tutor's sheet
func (s *AssessmentService) Submit(ctx context.Context, token, slug, sectionID, assignmentID, termID string, sent bool) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	var sh Sheet
	if assignmentID != "" {
		sh, _, err = s.sheetFor(ctx, sc, assignmentID, termID)
		sectionID = sh.Section.ID
	} else {
		sh, err = s.tutorSheetFor(ctx, sc, sectionID, termID)
	}
	if err != nil {
		return err
	}
	if sh.Locked {
		return domain.ErrTermClosed
	}
	if !sh.CanEdit {
		return domain.ErrNotYourSheet
	}
	if err := s.repo.SetSubmission(ctx, sc.institution.ID, sectionID, assignmentID, termID, sent, sc.userID, s.userName(sc)); err != nil {
		return err
	}
	s.changed(ctx, sc, "submissions", sectionID)
	return nil
}

// ---------- report cards ----------

type CardCompetency struct {
	ID         string
	Name       string
	Levels     map[string]string // term id → level
	Conclusion string            // of the selected term
}

type CardSubject struct {
	ID           string
	Name         string
	Kind         string
	TeacherName  string
	Competencies []CardCompetency
	// Numeric: average per term id (nil: no scores) and the evaluations of the selected term with the student's score
	Averages    map[string]*float64
	Evaluations []CardEvaluation
}

type CardEvaluation struct {
	Name   string
	Weight int
	Score  *int
}

type CardTerm struct {
	ID         string
	Name       string
	Status     string
	Attendance domain.Tally
	Conduct    *int
}

type ReportCard struct {
	Institution  string
	Kind         string
	Student      domain.StudentRef
	Section      domain.SectionMeta
	PeriodID     string
	PeriodName   string
	Scale        string
	Passing      *int
	Terms        []CardTerm
	TermID       string
	Subjects     []CardSubject
	Transversal  []CardCompetency
	TutorComment string
	// The selected term is still open (staff preview, or IEST/CEPRE grades in progress)
	Provisional bool
	// Other periods of the student, newest first (id → name)
	Periods []domain.Period
}

func (s *AssessmentService) ReportCard(ctx context.Context, token, slug, studentID, periodID, termID string) (ReportCard, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ReportCard{}, err
	}
	return s.reportCard(ctx, sc, studentID, periodID, termID)
}

func (s *AssessmentService) reportCard(ctx context.Context, sc gradeScope, studentID, periodID, termID string) (ReportCard, error) {
	if sc.family() {
		mine, err := s.attendance.ownStudents(ctx, sc.attendanceScope)
		if err != nil {
			return ReportCard{}, err
		}
		if !slices.ContainsFunc(mine, func(h domain.CardHolder) bool { return h.ID == studentID }) {
			return ReportCard{}, domain.ErrNotYourStudent
		}
	}
	periodIDs, err := s.repo.StudentPeriods(ctx, sc.institution.ID, studentID)
	if err != nil {
		return ReportCard{}, err
	}
	if len(periodIDs) == 0 {
		return ReportCard{}, domain.ErrNotFound
	}
	if periodID == "" {
		periodID = periodIDs[0]
	} else if !slices.Contains(periodIDs, periodID) {
		return ReportCard{}, domain.ErrNotFound
	}
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return ReportCard{}, err
	}
	sectionID, err := s.repo.StudentSection(ctx, sc.institution.ID, studentID, periodID)
	if err != nil {
		return ReportCard{}, err
	}
	meta, err := s.repo.Section(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return ReportCard{}, err
	}
	if !sc.family() && !sc.readsAll() && !slices.Contains(sc.tutoring, sectionID) {
		return ReportCard{}, domain.ErrNotYourSheet
	}
	// Families see closed terms only; staff see every term up to the selected one
	visible := p.Terms
	if sc.family() {
		visible = slices.DeleteFunc(slices.Clone(p.Terms), func(t domain.Term) bool { return t.Status != domain.TermClosed && p.Status != domain.PeriodClosed })
		if len(p.Terms) > 0 && len(visible) == 0 {
			return ReportCard{}, domain.ErrCardNotReady
		}
	}
	if termID == "" {
		if sc.family() && len(visible) > 0 {
			termID = visible[len(visible)-1].ID
		} else {
			termID = defaultTerm(p, s.attendance.today())
		}
	}
	term, locked, err := termOf(p, termID)
	if err != nil {
		return ReportCard{}, err
	}
	if sc.family() && len(p.Terms) > 0 && !locked {
		return ReportCard{}, domain.ErrCardNotReady
	}
	card := ReportCard{Institution: sc.institution.Name, Kind: sc.institution.Kind, Section: meta, PeriodID: p.ID, PeriodName: p.Name, Scale: sc.settings.GradingScale,
		Passing: sc.settings.PassingGrade, TermID: termID, Provisional: !locked}
	for _, id := range periodIDs {
		if x, err := s.academic.repo.Period(ctx, sc.institution.ID, id); err == nil {
			card.Periods = append(card.Periods, x)
		}
	}
	students, err := s.repo.Students(ctx, sc.institution.ID, sectionID, "")
	if err != nil {
		return ReportCard{}, err
	}
	for _, st := range students {
		if st.ID == studentID {
			card.Student = st
		}
	}
	if card.Student.ID == "" {
		// Withdrawn after the term: still has a card
		_, _, _, name, err := s.attendance.repo.StudentPeriod(ctx, sc.institution.ID, studentID)
		if err != nil {
			return ReportCard{}, err
		}
		card.Student = domain.StudentRef{ID: studentID, Name: name}
	}

	// Terms shown: up to the selected one (the whole period when it has none)
	terms := []domain.Term{term}
	if len(p.Terms) > 0 {
		terms = nil
		for _, t := range visible {
			terms = append(terms, t)
			if t.ID == termID {
				break
			}
		}
	}
	conduct := map[string]*int{}
	if sc.settings.ConductGrade && !sc.numeric() {
		cm, err := s.repo.Conduct(ctx, sc.institution.ID, sectionID, "", true)
		if err != nil {
			return card, err
		}
		for _, c := range cm {
			if c.StudentID == studentID {
				v := c.Score
				conduct[c.TermID] = &v
			}
		}
	}
	for _, t := range terms {
		tally, err := s.repo.AttendanceBetween(ctx, sc.institution.ID, studentID, t.StartsOn, t.EndsOn)
		if err != nil {
			return card, err
		}
		card.Terms = append(card.Terms, CardTerm{ID: t.ID, Name: t.Name, Status: t.Status, Attendance: tally, Conduct: conduct[t.ID]})
	}

	subjects, err := s.repo.SubjectsOfSection(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return card, err
	}
	if sc.numeric() {
		evals, err := s.repo.SectionEvaluations(ctx, sc.institution.ID, sectionID)
		if err != nil {
			return card, err
		}
		ids := make([]string, len(evals))
		for i, e := range evals {
			ids[i] = e.ID
		}
		scores, err := s.repo.Scores(ctx, ids)
		if err != nil {
			return card, err
		}
		mine := map[string]int{}
		for _, x := range scores {
			if x.StudentID == studentID {
				mine[x.EvaluationID] = x.Score
			}
		}
		for _, a := range subjects {
			if a.SubjectKind == "efsrt" {
				continue
			}
			row := CardSubject{ID: a.SubjectID, Name: a.SubjectName, Kind: a.SubjectKind, TeacherName: a.StaffName, Averages: map[string]*float64{}}
			any := false
			for _, t := range terms {
				var list []domain.Evaluation
				for _, e := range evals {
					if e.AssignmentID == a.ID && e.TermID == t.ID {
						list = append(list, e)
					}
				}
				row.Averages[t.ID] = WeightedAverage(list, mine)
				if t.ID == termID {
					for _, e := range list {
						ce := CardEvaluation{Name: e.Name, Weight: e.Weight}
						if v, ok := mine[e.ID]; ok {
							ce.Score = &v
						}
						row.Evaluations = append(row.Evaluations, ce)
					}
				}
				any = any || len(list) > 0
			}
			if any {
				card.Subjects = append(card.Subjects, row)
			}
		}
		return card, nil
	}

	marks, err := s.repo.CompetencyMarks(ctx, sc.institution.ID, sectionID, "", true)
	if err != nil {
		return card, err
	}
	byComp := map[string]map[string]domain.CompetencyMark{}
	for _, m := range marks {
		if m.StudentID != studentID {
			continue
		}
		if byComp[m.CompetencyID] == nil {
			byComp[m.CompetencyID] = map[string]domain.CompetencyMark{}
		}
		byComp[m.CompetencyID][m.TermID] = m
	}
	shown := map[string]bool{}
	for _, t := range terms {
		shown[t.ID] = true
	}
	toCard := func(c domain.CompetencyRef) CardCompetency {
		cc := CardCompetency{ID: c.ID, Name: c.Name, Levels: map[string]string{}}
		for tid, m := range byComp[c.ID] {
			if shown[tid] {
				cc.Levels[tid] = m.Level
			}
		}
		cc.Conclusion = byComp[c.ID][termID].Conclusion
		return cc
	}
	for _, a := range subjects {
		comps, err := s.repo.Competencies(ctx, sc.institution.ID, a.SubjectID)
		if err != nil {
			return card, err
		}
		if len(comps) == 0 {
			continue
		}
		row := CardSubject{ID: a.SubjectID, Name: a.SubjectName, Kind: a.SubjectKind, TeacherName: a.StaffName}
		for _, c := range comps {
			row.Competencies = append(row.Competencies, toCard(c))
		}
		card.Subjects = append(card.Subjects, row)
	}
	trans, err := s.repo.Transversal(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return card, err
	}
	for _, c := range trans {
		card.Transversal = append(card.Transversal, toCard(c))
	}
	comments, err := s.repo.Comments(ctx, sectionID, termID)
	if err != nil {
		return card, err
	}
	for _, c := range comments {
		if c.StudentID == studentID {
			card.TutorComment = c.Comment
		}
	}
	return card, nil
}

// WeightedAverage is the average of the student's scores weighted by each evaluation (nil without scores). Evaluations
// not taken yet do not count
func WeightedAverage(evals []domain.Evaluation, scores map[string]int) *float64 {
	sum, weights := 0, 0
	for _, e := range evals {
		if v, ok := scores[e.ID]; ok {
			sum += v * e.Weight
			weights += e.Weight
		}
	}
	if weights == 0 {
		return nil
	}
	avg := math.Round(float64(sum)/float64(weights)*10) / 10
	return &avg
}

// RoundGrade rounds an average to the whole grade the way Peruvian registers do (0.5 goes up)
func RoundGrade(avg float64) int { return int(math.Floor(avg + 0.5)) }

// SectionCards are the report cards of every student of a section, for printing
func (s *AssessmentService) SectionCards(ctx context.Context, token, slug, sectionID, termID string) ([]ReportCard, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if sc.family() || !sc.readsAll() && !slices.Contains(sc.tutoring, sectionID) {
		return nil, domain.ErrNotYourSheet
	}
	meta, err := s.repo.Section(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return nil, err
	}
	students, err := s.repo.Students(ctx, sc.institution.ID, sectionID, "")
	if err != nil {
		return nil, err
	}
	out := make([]ReportCard, 0, len(students))
	for _, st := range students {
		c, err := s.reportCard(ctx, sc, st.ID, meta.PeriodID, termID)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// MyCards: families get each child's latest report card they may see (closed terms; IEST/CEPRE grades in progress)
type FamilyCard struct {
	Student domain.CardHolder
	Card    *ReportCard
	// Why there is no card yet
	Message string
}

func (s *AssessmentService) MyCards(ctx context.Context, token, slug string) ([]FamilyCard, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if !sc.family() {
		return nil, nil
	}
	mine, err := s.attendance.ownStudents(ctx, sc.attendanceScope)
	if err != nil {
		return nil, err
	}
	out := make([]FamilyCard, 0, len(mine))
	for _, h := range mine {
		c, err := s.reportCard(ctx, sc, h.ID, "", "")
		var de *domain.Error
		switch {
		case err == nil:
			out = append(out, FamilyCard{Student: h, Card: &c})
		case errors.As(err, &de):
			out = append(out, FamilyCard{Student: h, Message: de.Message})
		default:
			return nil, err
		}
	}
	return out, nil
}
