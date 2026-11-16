package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ClosingService closes the year (phase 7 of docs/plan-academico.md): the final situation of each student by the
// rules of RVM 094-2020-MINEDU with the evaluation team's decisions, the summer recovery, the final result of each
// IEST unit, the actas and the export for SIAGIE. Dirección and coordinación run it; secretaría reads and prints
type ClosingService struct {
	repo       port.ClosingRepository
	assessment *AssessmentService
}

func NewClosingService(repo port.ClosingRepository, assessment *AssessmentService) *ClosingService {
	return &ClosingService{repo: repo, assessment: assessment}
}

// Closing modes: Educación Básica (situation per student) or units with scores (IEST)
const (
	ClosingBasic = "basic"
	ClosingUnits = "units"
)

func (s *ClosingService) scope(ctx context.Context, token, slug string) (gradeScope, error) {
	sc, err := s.assessment.scope(ctx, token, slug)
	if err != nil {
		return sc, err
	}
	if !sc.readsAll() || gradeReaders[sc.role] && sc.role != "secretary" {
		return sc, domain.ErrClosingForbidden
	}
	if sc.institution.Kind == domain.KindCEPRE {
		return sc, domain.ErrNoClosingForKind
	}
	return sc, nil
}

func (s *ClosingService) manage(ctx context.Context, token, slug string) (gradeScope, error) {
	sc, err := s.scope(ctx, token, slug)
	if err == nil && !sc.manager() {
		return sc, domain.ErrClosingForbidden
	}
	return sc, err
}

func mode(sc gradeScope) string {
	if sc.numeric() {
		return ClosingUnits
	}
	return ClosingBasic
}

func (s *ClosingService) changed(ctx context.Context, sc gradeScope, sectionID string) {
	s.assessment.academic.live.Notify(ctx, EventAssessmentChanged, map[string]string{"institution": sc.institution.Slug, "area": "closing", "section": sectionID})
}

// lastTerm is the last term of the period ("" when it has none) and whether grading there is closed
func lastTerm(p domain.Period) (string, bool) {
	if len(p.Terms) == 0 {
		return "", p.Status == domain.PeriodClosed
	}
	t := p.Terms[len(p.Terms)-1]
	return t.ID, t.Status == domain.TermClosed || p.Status == domain.PeriodClosed
}

// ---------- overview ----------

type SectionClosing struct {
	SectionID string
	Label     string
	Students  int
	// Situation in force → students (PRO, PER, RR, PEN)
	Counts    map[string]int
	Confirmed int
	Recovered int
}

type UnitClosing struct {
	AssignmentID string
	SectionID    string
	SectionLabel string
	SubjectName  string
	StaffName    string
	Students     int
	Confirmed    bool
	Counts       map[string]int
}

type ClosingOverview struct {
	Mode           string
	Periods        []domain.Period
	Period         domain.Period
	LastTermClosed bool
	CanManage      bool
	Sections       []SectionClosing
	Units          []UnitClosing
	Records        []domain.Record
}

func (s *ClosingService) Overview(ctx context.Context, token, slug, periodID string) (ClosingOverview, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ClosingOverview{}, err
	}
	p, periods, err := s.assessment.period(ctx, sc, periodID)
	if err != nil {
		return ClosingOverview{}, err
	}
	out := ClosingOverview{Mode: mode(sc), Periods: periods, Period: p, CanManage: sc.manager()}
	if p.ID == "" {
		return out, nil
	}
	_, out.LastTermClosed = lastTerm(p)
	if out.Records, err = s.repo.Records(ctx, sc.institution.ID, p.ID); err != nil {
		return out, err
	}
	sections, err := s.assessment.academic.repo.Sections(ctx, sc.institution.ID, p.ID)
	if err != nil {
		return out, err
	}
	if out.Mode == ClosingUnits {
		for _, sec := range sections {
			subjects, err := s.assessment.repo.SubjectsOfSection(ctx, sc.institution.ID, sec.ID)
			if err != nil {
				return out, err
			}
			meta, err := s.assessment.repo.Section(ctx, sc.institution.ID, sec.ID)
			if err != nil {
				return out, err
			}
			for _, a := range subjects {
				if a.SubjectKind == "efsrt" {
					continue
				}
				u := UnitClosing{AssignmentID: a.ID, SectionID: sec.ID, SectionLabel: meta.Label + " · " + meta.LevelName, SubjectName: a.SubjectName, StaffName: a.StaffName, Counts: map[string]int{}}
				students, err := s.assessment.repo.Students(ctx, sc.institution.ID, sec.ID, a.SubjectID)
				if err != nil {
					return out, err
				}
				u.Students = len(students)
				results, err := s.repo.UnitResults(ctx, sc.institution.ID, a.ID)
				if err != nil {
					return out, err
				}
				u.Confirmed = len(results) > 0
				for _, r := range results {
					u.Counts[r.Status]++
				}
				if u.Students > 0 {
					out.Units = append(out.Units, u)
				}
			}
		}
		return out, nil
	}
	for _, sec := range sections {
		res, err := s.section(ctx, sc, p, sec.ID)
		if err != nil {
			return out, err
		}
		if len(res.Rows) == 0 {
			continue
		}
		sum := SectionClosing{SectionID: sec.ID, Label: res.Section.Label + " · " + res.Section.LevelName, Students: len(res.Rows), Counts: map[string]int{}}
		for _, r := range res.Rows {
			sum.Counts[r.Situation]++
			if r.Confirmed {
				sum.Confirmed++
			}
			if r.AfterRecovery != "" {
				sum.Recovered++
			}
		}
		out.Sections = append(out.Sections, sum)
	}
	return out, nil
}

// ---------- one section (Educación Básica) ----------

type ClosingArea struct {
	SubjectID    string
	SubjectCode  string
	Name         string
	Competencies []domain.CompetencyRef
}

type ClosingRow struct {
	Student domain.StudentRef
	// competency id → final level (the last term)
	Final    map[string]string
	Computed string
	Reason   string
	// The team's decision ('' = the rules)
	Decided      string
	DecisionNote string
	DecidedName  string
	Situation    string
	Confirmed    bool
	// Recovery: competencies to recover, the levels reached and the definitive situation
	Targets       []string
	Recovery      map[string]string
	AfterRecovery string
	// Phase E7: the enrollment, the student's own grade (multigrade sections), the Religion exemption and the SIAGIE
	// state of the grades
	EnrollmentID   string
	GradeShort     string
	ReligionExempt bool
	SiagieStatus   string
	SiagieNote     string
	SiagieAt       *time.Time
}

type SectionResult struct {
	Section        domain.SectionMeta
	Level          domain.SectionLevel
	Rule           PromotionRule
	Period         domain.Period
	LastTermClosed bool
	CanManage      bool
	Areas          []ClosingArea
	Rows           []ClosingRow
	Records        []domain.Record
}

func (s *ClosingService) Section(ctx context.Context, token, slug, sectionID string) (SectionResult, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return SectionResult{}, err
	}
	if sc.numeric() {
		return SectionResult{}, domain.ErrWrongClosingScale
	}
	meta, err := s.assessment.repo.Section(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return SectionResult{}, err
	}
	p, err := s.assessment.academic.repo.Period(ctx, sc.institution.ID, meta.PeriodID)
	if err != nil {
		return SectionResult{}, err
	}
	res, err := s.section(ctx, sc, p, sectionID)
	if err != nil {
		return res, err
	}
	records, err := s.repo.Records(ctx, sc.institution.ID, p.ID)
	if err != nil {
		return res, err
	}
	for _, r := range records {
		if r.SectionID == sectionID {
			res.Records = append(res.Records, r)
		}
	}
	return res, nil
}

func (s *ClosingService) section(ctx context.Context, sc gradeScope, p domain.Period, sectionID string) (SectionResult, error) {
	inst := sc.institution.ID
	meta, err := s.assessment.repo.Section(ctx, inst, sectionID)
	if err != nil {
		return SectionResult{}, err
	}
	level, err := s.repo.SectionLevel(ctx, inst, sectionID)
	if err != nil {
		return SectionResult{}, err
	}
	termID, closed := lastTerm(p)
	rule, err := s.rule(ctx, sc, level)
	if err != nil {
		return SectionResult{}, err
	}
	res := SectionResult{Section: meta, Level: level, Rule: rule, Period: p, LastTermClosed: closed, CanManage: sc.manager()}
	subjects, err := s.assessment.repo.SubjectsOfSection(ctx, inst, sectionID)
	if err != nil {
		return res, err
	}
	for _, a := range subjects {
		comps, err := s.assessment.repo.Competencies(ctx, inst, a.SubjectID)
		if err != nil {
			return res, err
		}
		if len(comps) > 0 {
			res.Areas = append(res.Areas, ClosingArea{SubjectID: a.SubjectID, SubjectCode: a.SubjectCode, Name: a.SubjectName, Competencies: comps})
		}
	}
	students, err := s.assessment.repo.Students(ctx, inst, sectionID, "")
	if err != nil {
		return res, err
	}
	marks, err := s.assessment.repo.CompetencyMarks(ctx, inst, sectionID, termID, false)
	if err != nil {
		return res, err
	}
	final := map[string]map[string]string{}
	for _, m := range marks {
		if final[m.StudentID] == nil {
			final[m.StudentID] = map[string]string{}
		}
		final[m.StudentID][m.CompetencyID] = m.Level
	}
	stored, err := s.repo.FinalResults(ctx, inst, p.ID, sectionID)
	if err != nil {
		return res, err
	}
	byStudent := map[string]domain.FinalResult{}
	for _, f := range stored {
		byStudent[f.StudentID] = f
	}
	ids := make([]string, len(students))
	for i, st := range students {
		ids[i] = st.ID
	}
	recovery, err := s.repo.RecoveryLevels(ctx, p.ID, ids)
	if err != nil {
		return res, err
	}
	info, err := s.repo.ClosingStudents(ctx, inst, sectionID)
	if err != nil {
		return res, err
	}
	for _, st := range students {
		in := info[st.ID]
		row := ClosingRow{Student: st, Final: final[st.ID], Recovery: recovery[st.ID], EnrollmentID: in.EnrollmentID, GradeShort: in.GradeShort,
			ReligionExempt: in.ReligionExempt, SiagieStatus: in.SiagieStatus, SiagieNote: in.SiagieNote, SiagieAt: in.SiagieAt}
		if row.SiagieStatus == "" {
			row.SiagieStatus = domain.SiagiePending
		}
		if row.Final == nil {
			row.Final = map[string]string{}
		}
		if row.Recovery == nil {
			row.Recovery = map[string]string{}
		}
		row.Computed, row.Reason = FinalSituation(res.Rule, res.areaLevels(row.Final, nil, row.ReligionExempt))
		f, ok := byStudent[st.ID]
		if ok {
			row.Decided, row.DecisionNote, row.DecidedName = f.Decided, f.DecisionNote, f.DecidedName
			row.Confirmed = f.ConfirmedAt != nil
			row.AfterRecovery = f.AfterRecovery
		}
		row.Situation = row.Computed
		if row.Decided != "" {
			row.Situation = row.Decided
		}
		if row.Situation == SituationRecovery {
			row.Targets = res.recoveryTargets(row.Final)
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

// areaLevels lists the final levels per area, with the recovery levels over them when given; a student exempted
// from Educación Religiosa leaves that area out
func (r SectionResult) areaLevels(final, recovery map[string]string, exempt bool) []AreaLevels {
	out := make([]AreaLevels, 0, len(r.Areas))
	for _, a := range r.Areas {
		if exempt && a.SubjectCode == domain.ReligionSubjectCode {
			continue
		}
		al := AreaLevels{Name: a.Name}
		for _, c := range a.Competencies {
			l := final[c.ID]
			if v, ok := recovery[c.ID]; ok && v != "" {
				l = v
			}
			al.Levels = append(al.Levels, l)
		}
		out = append(out, al)
	}
	return out
}

// recoveryTargets are the competencies in C; with none (promotion failed for lack of A), the ones in B
func (r SectionResult) recoveryTargets(final map[string]string) []string {
	var c, b []string
	for _, a := range r.Areas {
		for _, comp := range a.Competencies {
			switch final[comp.ID] {
			case "C":
				c = append(c, comp.ID)
			case "B":
				b = append(b, comp.ID)
			}
		}
	}
	if len(c) > 0 {
		return c
	}
	return b
}

// Decide records the evaluation team's situation for a student (empty clears it); the reason is required
func (s *ClosingService) Decide(ctx context.Context, token, slug, sectionID, studentID, situation, note string) (SectionResult, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return SectionResult{}, err
	}
	res, err := s.Section(ctx, token, slug, sectionID)
	if err != nil {
		return res, err
	}
	if !slices.ContainsFunc(res.Rows, func(r ClosingRow) bool { return r.Student.ID == studentID }) {
		return res, domain.ErrNotFound
	}
	note = strings.TrimSpace(note)
	switch {
	case situation != "" && !slices.Contains([]string{SituationPromoted, SituationRepeat, SituationRecovery}, situation):
		return res, domain.Validation(map[string]string{"situation": "Elige promovido, permanece o recuperación."})
	case situation != "" && note == "":
		return res, domain.Validation(map[string]string{"note": "Registra el motivo de la decisión del equipo de evaluación."})
	case len([]rune(note)) > 500:
		return res, domain.Validation(map[string]string{"note": "El motivo tiene hasta 500 caracteres."})
	}
	if situation == "" {
		note = ""
	}
	if err := s.repo.SaveDecision(ctx, sc.institution.ID, res.Period.ID, sectionID, studentID, situation, note, s.assessment.userName(sc)); err != nil {
		return res, err
	}
	s.changed(ctx, sc, sectionID)
	return s.Section(ctx, token, slug, sectionID)
}

// Confirm stores the situation of every student of the section; the last term must be closed and nobody pending
func (s *ClosingService) Confirm(ctx context.Context, token, slug, sectionID string) (SectionResult, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return SectionResult{}, err
	}
	res, err := s.Section(ctx, token, slug, sectionID)
	if err != nil {
		return res, err
	}
	if !res.LastTermClosed {
		return res, domain.ErrLastTermOpen
	}
	rows := make([]port.FinalRow, 0, len(res.Rows))
	for _, r := range res.Rows {
		if r.Situation == SituationPending {
			return res, domain.ErrPendingLevels
		}
		rows = append(rows, port.FinalRow{StudentID: r.Student.ID, Computed: r.Computed, Reason: r.Reason})
	}
	if err := s.repo.ConfirmResults(ctx, sc.institution.ID, res.Period.ID, sectionID, rows); err != nil {
		return res, err
	}
	s.changed(ctx, sc, sectionID)
	return s.Section(ctx, token, slug, sectionID)
}

// SaveRecovery stores the levels reached in recovery; once every target has one, the definitive situation follows
func (s *ClosingService) SaveRecovery(ctx context.Context, token, slug, sectionID, studentID string, levels map[string]string) (SectionResult, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return SectionResult{}, err
	}
	res, err := s.Section(ctx, token, slug, sectionID)
	if err != nil {
		return res, err
	}
	i := slices.IndexFunc(res.Rows, func(r ClosingRow) bool { return r.Student.ID == studentID })
	if i < 0 {
		return res, domain.ErrNotFound
	}
	row := res.Rows[i]
	if !row.Confirmed {
		return res, domain.ErrNotConfirmed
	}
	if row.Situation != SituationRecovery {
		return res, domain.ErrNotInRecovery
	}
	merged := map[string]string{}
	for k, v := range row.Recovery {
		merged[k] = v
	}
	for comp, level := range levels {
		if !slices.Contains(row.Targets, comp) {
			return res, domain.Validation(map[string]string{"levels": "Solo se recuperan las competencias que terminaron en C (o en B si no hubo C)."})
		}
		if level != "" && !slices.Contains(domain.Levels, level) {
			return res, domain.Validation(map[string]string{"levels": "El nivel de logro es AD, A, B o C."})
		}
		merged[comp] = level
	}
	after := ""
	complete := true
	for _, t := range row.Targets {
		if merged[t] == "" {
			complete = false
		}
	}
	if complete {
		after, _ = AfterRecovery(res.Rule, res.areaLevels(row.Final, merged, row.ReligionExempt))
	}
	if err := s.repo.SaveRecovery(ctx, res.Period.ID, studentID, levels, after); err != nil {
		return res, err
	}
	s.changed(ctx, sc, sectionID)
	return s.Section(ctx, token, slug, sectionID)
}

// ---------- units (IEST) ----------

type UnitRow struct {
	Student domain.StudentRef
	// evaluation id → score
	Scores        map[string]int
	Average       *float64
	Grade         *int
	Missing       int
	AbsentPercent float64
	Status        string
	RecoveryScore *int
	FinalGrade    *int
	Confirmed     bool
}

type UnitResultSheet struct {
	Assignment  domain.Assignment
	Section     domain.SectionMeta
	Period      domain.Period
	Passing     int
	Limit       int
	CanManage   bool
	Evaluations []domain.Evaluation
	Rows        []UnitRow
	Records     []domain.Record
}

func (s *ClosingService) Unit(ctx context.Context, token, slug, assignmentID string) (UnitResultSheet, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return UnitResultSheet{}, err
	}
	return s.unit(ctx, sc, assignmentID)
}

func (s *ClosingService) unit(ctx context.Context, sc gradeScope, assignmentID string) (UnitResultSheet, error) {
	if !sc.numeric() {
		return UnitResultSheet{}, domain.ErrWrongClosingScale
	}
	inst := sc.institution.ID
	a, err := s.assessment.teaching.Assignment(ctx, inst, assignmentID)
	if err != nil {
		return UnitResultSheet{}, err
	}
	meta, err := s.assessment.repo.Section(ctx, inst, a.SectionID)
	if err != nil {
		return UnitResultSheet{}, err
	}
	p, err := s.assessment.academic.repo.Period(ctx, inst, a.PeriodID)
	if err != nil {
		return UnitResultSheet{}, err
	}
	passing := 13
	if sc.settings.PassingGrade != nil {
		passing = *sc.settings.PassingGrade
	}
	out := UnitResultSheet{Assignment: a, Section: meta, Period: p, Passing: passing, Limit: sc.settings.AbsenceLimitPercent, CanManage: sc.manager()}
	all, err := s.assessment.repo.SectionEvaluations(ctx, inst, a.SectionID)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for _, e := range all {
		if e.AssignmentID == a.ID {
			out.Evaluations = append(out.Evaluations, e)
			ids = append(ids, e.ID)
		}
	}
	scores, err := s.assessment.repo.Scores(ctx, ids)
	if err != nil {
		return out, err
	}
	byStudent := map[string]map[string]int{}
	for _, x := range scores {
		if byStudent[x.StudentID] == nil {
			byStudent[x.StudentID] = map[string]int{}
		}
		byStudent[x.StudentID][x.EvaluationID] = x.Score
	}
	tallies, err := s.assessment.attendance.repo.SubjectTallies(ctx, inst, p.ID, a.SectionID, "")
	if err != nil {
		return out, err
	}
	absent := map[string]float64{}
	for _, t := range tallies {
		if t.AssignmentID == a.ID {
			absent[t.StudentID] = t.Classes.AbsentPercent()
		}
	}
	stored, err := s.repo.UnitResults(ctx, inst, a.ID)
	if err != nil {
		return out, err
	}
	saved := map[string]domain.UnitResult{}
	for _, u := range stored {
		saved[u.StudentID] = u
	}
	students, err := s.assessment.repo.Students(ctx, inst, a.SectionID, a.SubjectID)
	if err != nil {
		return out, err
	}
	for _, st := range students {
		row := UnitRow{Student: st, Scores: byStudent[st.ID], Average: WeightedAverage(out.Evaluations, byStudent[st.ID]), AbsentPercent: absent[st.ID]}
		row.Missing = len(out.Evaluations) - len(byStudent[st.ID])
		if row.Average != nil {
			g := RoundGrade(*row.Average)
			row.Grade = &g
		}
		row.Status = UnitStatus(row.Grade, passing, row.AbsentPercent, out.Limit)
		row.FinalGrade = row.Grade
		if u, ok := saved[st.ID]; ok {
			row.Confirmed = true
			if u.RecoveryScore != nil && row.Status == UnitRecovery {
				row.RecoveryScore, row.FinalGrade, row.Status = u.RecoveryScore, u.FinalGrade, u.Status
			}
		}
		out.Rows = append(out.Rows, row)
	}
	records, err := s.repo.Records(ctx, inst, p.ID)
	if err != nil {
		return out, err
	}
	for _, r := range records {
		if r.AssignmentID == a.ID {
			out.Records = append(out.Records, r)
		}
	}
	return out, nil
}

func (s *ClosingService) ConfirmUnit(ctx context.Context, token, slug, assignmentID string) (UnitResultSheet, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return UnitResultSheet{}, err
	}
	u, err := s.unit(ctx, sc, assignmentID)
	if err != nil {
		return u, err
	}
	if len(u.Rows) == 0 {
		return u, domain.ErrNothingToRecord
	}
	rows := make([]domain.UnitResult, len(u.Rows))
	for i, r := range u.Rows {
		status := UnitStatus(r.Grade, u.Passing, r.AbsentPercent, u.Limit)
		rows[i] = domain.UnitResult{StudentID: r.Student.ID, Average: r.Average, Grade: r.Grade, AbsentPercent: r.AbsentPercent, Status: status}
	}
	if err := s.repo.ConfirmUnit(ctx, sc.institution.ID, assignmentID, rows); err != nil {
		return u, err
	}
	s.changed(ctx, sc, u.Section.ID)
	return s.unit(ctx, sc, assignmentID)
}

// SaveUnitRecovery records the recovery evaluation of a student (nil clears it)
func (s *ClosingService) SaveUnitRecovery(ctx context.Context, token, slug, assignmentID, studentID string, score *int) (UnitResultSheet, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return UnitResultSheet{}, err
	}
	u, err := s.unit(ctx, sc, assignmentID)
	if err != nil {
		return u, err
	}
	i := slices.IndexFunc(u.Rows, func(r UnitRow) bool { return r.Student.ID == studentID })
	if i < 0 {
		return u, domain.ErrNotFound
	}
	row := u.Rows[i]
	if !row.Confirmed {
		return u, domain.ErrUnitNotConfirmed
	}
	if UnitStatus(row.Grade, u.Passing, row.AbsentPercent, u.Limit) != UnitRecovery {
		return u, domain.ErrNotUnitRecovery
	}
	status := UnitRecovery
	var final *int
	if score != nil {
		if *score < 0 || *score > 20 {
			return u, domain.Validation(map[string]string{"score": "La nota de recuperación va de 0 a 20."})
		}
		f, st := UnitAfterRecovery(row.Grade, *score, u.Passing)
		final, status = &f, st
	}
	if err := s.repo.SaveUnitRecovery(ctx, assignmentID, studentID, score, final, status); err != nil {
		return u, err
	}
	s.changed(ctx, sc, u.Section.ID)
	return s.unit(ctx, sc, assignmentID)
}

// ---------- records (actas) ----------

// RecordContent is the frozen body of an acta; the app prints it
type RecordContent struct {
	Title       string          `json:"title"`
	Institution string          `json:"institution"`
	Period      string          `json:"period"`
	Section     string          `json:"section"`
	Level       string          `json:"level"`
	Subject     string          `json:"subject,omitempty"`
	Teacher     string          `json:"teacher,omitempty"`
	Tutor       string          `json:"tutor,omitempty"`
	Rule        string          `json:"rule,omitempty"`
	Groups      []RecordGroup   `json:"groups"`
	Rows        []RecordRow     `json:"rows"`
	Summary     []RecordSummary `json:"summary"`
	Legend      []string        `json:"legend"`
	Signatures  []string        `json:"signatures"`
	IssuedOn    string          `json:"issuedOn"`
}

type RecordGroup struct {
	Name    string         `json:"name"`
	Columns []RecordColumn `json:"columns"`
}

type RecordColumn struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type RecordRow struct {
	N         int      `json:"n"`
	Grade     string   `json:"grade,omitempty"`
	Code      string   `json:"code"`
	Document  string   `json:"document"`
	Name      string   `json:"name"`
	Cells     []string `json:"cells"`
	Situation string   `json:"situation"`
	Note      string   `json:"note,omitempty"`
}

type RecordSummary struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

var situationNames = map[string]string{SituationPromoted: "Promovido", SituationRepeat: "Permanece en el grado", SituationRecovery: "Recuperación pedagógica"}

func (s *ClosingService) Issue(ctx context.Context, token, slug, sectionID, assignmentID, kind string) (domain.Record, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return domain.Record{}, err
	}
	today := s.assessment.attendance.today().Format(time.DateOnly)
	var content RecordContent
	var periodID string
	switch kind {
	case "final", "complementary":
		res, err := s.Section(ctx, token, slug, sectionID)
		if err != nil {
			return domain.Record{}, err
		}
		periodID = res.Period.ID
		content, err = basicRecord(sc, res, kind, today)
		if err != nil {
			return domain.Record{}, err
		}
	case "unit":
		u, err := s.unit(ctx, sc, assignmentID)
		if err != nil {
			return domain.Record{}, err
		}
		sectionID, periodID = u.Section.ID, u.Period.ID
		content, err = unitRecord(sc, u, today)
		if err != nil {
			return domain.Record{}, err
		}
	default:
		return domain.Record{}, domain.Validation(map[string]string{"kind": "Elige el tipo de acta."})
	}
	body, err := json.Marshal(content)
	if err != nil {
		return domain.Record{}, err
	}
	rec, err := s.repo.IssueRecord(ctx, sc.institution.ID, domain.Record{PeriodID: periodID, SectionID: sectionID, AssignmentID: assignmentID, Kind: kind, Content: body, IssuedName: s.assessment.userName(sc)}, sc.userID)
	if err != nil {
		return rec, err
	}
	s.changed(ctx, sc, sectionID)
	return rec, nil
}

// mixedGrades tells a multigrade section that holds students of several grades: the acta then shows each one's
func mixedGrades(rows []ClosingRow) bool {
	for _, r := range rows {
		if r.GradeShort != rows[0].GradeShort {
			return true
		}
	}
	return false
}

func basicRecord(sc gradeScope, res SectionResult, kind, today string) (RecordContent, error) {
	c := RecordContent{Institution: sc.institution.Name, Period: res.Period.Name, Section: res.Section.Label, Level: res.Section.LevelName, Tutor: res.Section.TutorName, Rule: res.Rule.Name,
		IssuedOn: today, Signatures: []string{"Tutor(a)", "Director(a)"},
		Legend: []string{"AD: logro destacado", "A: logro esperado", "B: en proceso", "C: en inicio", "Normativa: RVM N.° 094-2020-MINEDU"}}
	for i, a := range res.Areas {
		g := RecordGroup{Name: a.Name}
		for j, comp := range a.Competencies {
			g.Columns = append(g.Columns, RecordColumn{Code: fmt.Sprintf("%d.%d", i+1, j+1), Name: comp.Name})
		}
		c.Groups = append(c.Groups, g)
	}
	counts := map[string]int{}
	n := 0
	for _, r := range res.Rows {
		if !r.Confirmed {
			return c, domain.ErrNotConfirmed
		}
		if kind == "complementary" && r.Situation != SituationRecovery {
			continue
		}
		row := RecordRow{Code: r.Student.Code, Document: r.Student.Document, Name: r.Student.Name}
		if mixedGrades(res.Rows) {
			row.Grade = r.GradeShort
		}
		for _, a := range res.Areas {
			exempt := r.ReligionExempt && a.SubjectCode == domain.ReligionSubjectCode
			for _, comp := range a.Competencies {
				l := r.Final[comp.ID]
				if exempt {
					row.Cells = append(row.Cells, Exempt)
					continue
				}
				if kind == "complementary" {
					if !slices.Contains(r.Targets, comp.ID) {
						l = ""
					} else if v := r.Recovery[comp.ID]; v != "" {
						l = v
					} else {
						l = "—"
					}
				}
				row.Cells = append(row.Cells, l)
			}
		}
		situation := r.Situation
		if kind == "complementary" {
			if r.AfterRecovery == "" {
				return c, domain.Validation(map[string]string{"recovery": "Falta registrar la recuperación de " + r.Student.Name + "."})
			}
			situation = r.AfterRecovery
		}
		row.Situation = situation
		if r.Decided != "" && kind == "final" {
			row.Note = "Decisión del equipo de evaluación: " + r.DecisionNote
		}
		n++
		row.N = n
		counts[situation]++
		c.Rows = append(c.Rows, row)
	}
	if len(c.Rows) == 0 {
		return c, domain.ErrNothingToRecord
	}
	if slices.ContainsFunc(res.Rows, func(r ClosingRow) bool { return r.ReligionExempt }) {
		c.Legend = append(c.Legend, "EXO: exonerado de Educación Religiosa (Ley N.° 29635)")
	}
	if kind == "final" {
		c.Title = "Acta consolidada de evaluación integral"
	} else {
		c.Title = "Acta de evaluación de recuperación pedagógica"
		c.Legend = append(c.Legend, "Se muestran solo las competencias evaluadas en recuperación.")
	}
	for _, k := range []string{SituationPromoted, SituationRecovery, SituationRepeat} {
		if counts[k] > 0 {
			c.Summary = append(c.Summary, RecordSummary{Label: situationNames[k], Count: counts[k]})
		}
	}
	return c, nil
}

var unitStatusNames = map[string]string{UnitApproved: "Aprobado", UnitRecovery: "Recuperación", UnitFailed: "Desaprobado", UnitBarred: "Inhabilitado por inasistencia"}

func unitRecord(sc gradeScope, u UnitResultSheet, today string) (RecordContent, error) {
	c := RecordContent{Title: "Acta de evaluación de la unidad didáctica", Institution: sc.institution.Name, Period: u.Period.Name, Section: u.Section.Label, Level: u.Section.LevelName,
		Subject: u.Assignment.SubjectName, Teacher: u.Assignment.StaffName, IssuedOn: today, Signatures: []string{"Docente", "Coordinación académica"},
		Legend: []string{fmt.Sprintf("Nota aprobatoria: %d", u.Passing), fmt.Sprintf("Inhabilitado: %d %% o más de inasistencias", u.Limit)}}
	g := RecordGroup{Name: "Evaluaciones"}
	for _, e := range u.Evaluations {
		g.Columns = append(g.Columns, RecordColumn{Code: fmt.Sprintf("P%d", e.Weight), Name: e.Name})
	}
	c.Groups = []RecordGroup{g, {Name: "Resultado", Columns: []RecordColumn{{Code: "PROM", Name: "Promedio"}, {Code: "REC", Name: "Recuperación"}, {Code: "FINAL", Name: "Nota final"}}}}
	counts := map[string]int{}
	for i, r := range u.Rows {
		if !r.Confirmed {
			return c, domain.ErrUnitNotConfirmed
		}
		row := RecordRow{N: i + 1, Code: r.Student.Code, Document: r.Student.Document, Name: r.Student.Name, Situation: unitStatusNames[r.Status]}
		for _, e := range u.Evaluations {
			if v, ok := r.Scores[e.ID]; ok {
				row.Cells = append(row.Cells, fmt.Sprintf("%02d", v))
			} else {
				row.Cells = append(row.Cells, "—")
			}
		}
		row.Cells = append(row.Cells, gradeText(r.Grade), gradeText(r.RecoveryScore), gradeText(r.FinalGrade))
		if r.AbsentPercent > 0 {
			row.Note = fmt.Sprintf("Inasistencias: %.0f %%", r.AbsentPercent)
		}
		counts[row.Situation]++
		c.Rows = append(c.Rows, row)
	}
	for _, k := range []string{UnitApproved, UnitRecovery, UnitFailed, UnitBarred} {
		if counts[unitStatusNames[k]] > 0 {
			c.Summary = append(c.Summary, RecordSummary{Label: unitStatusNames[k], Count: counts[unitStatusNames[k]]})
		}
	}
	return c, nil
}

func gradeText(v *int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%02d", *v)
}

func (s *ClosingService) Records(ctx context.Context, token, slug, periodID string) ([]domain.Record, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	return s.repo.Records(ctx, sc.institution.ID, periodID)
}

func (s *ClosingService) Record(ctx context.Context, token, slug, id string) (domain.Record, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Record{}, err
	}
	return s.repo.Record(ctx, sc.institution.ID, id)
}

// ---------- export for SIAGIE ----------

// Export is a CSV (semicolon, UTF-8 with BOM, opens in Excel) with the final levels of a section (or the results of a
// unit), ready to copy into the SIAGIE loading templates
func (s *ClosingService) Export(ctx context.Context, token, slug, sectionID, assignmentID string) (string, []byte, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return "", nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF")
	w := csv.NewWriter(&buf)
	w.Comma = ';'
	var name string
	if assignmentID != "" {
		u, err := s.unit(ctx, sc, assignmentID)
		if err != nil {
			return "", nil, err
		}
		name = fmt.Sprintf("notas-%s-%s.csv", slugify(u.Assignment.SubjectName), slugify(u.Section.Label))
		_ = w.Write([]string{"N.°", "Código", "DNI", "Apellidos y nombres", "Unidad didáctica", "Promedio", "Recuperación", "Nota final", "Inasistencias (%)", "Situación"})
		for i, r := range u.Rows {
			avg := ""
			if r.Average != nil {
				avg = strings.Replace(fmt.Sprintf("%.1f", *r.Average), ".", ",", 1)
			}
			_ = w.Write([]string{fmt.Sprint(i + 1), r.Student.Code, r.Student.Document, r.Student.Name, u.Assignment.SubjectName, avg, gradeText(r.RecoveryScore), gradeText(r.FinalGrade),
				fmt.Sprintf("%.0f", r.AbsentPercent), unitStatusNames[r.Status]})
		}
	} else {
		if sc.numeric() {
			return "", nil, domain.ErrWrongClosingScale
		}
		res, err := s.Section(ctx, token, slug, sectionID)
		if err != nil {
			return "", nil, err
		}
		name = fmt.Sprintf("siagie-%s.csv", slugify(res.Section.Label+" "+res.Section.LevelName))
		_ = w.Write([]string{"N.°", "Código", "DNI", "Apellidos y nombres", "Área", "Competencia", "Nivel final", "Nivel tras recuperación", "Situación final"})
		for i, r := range res.Rows {
			situation := r.Situation
			if r.AfterRecovery != "" {
				situation = r.AfterRecovery
			}
			for _, a := range res.Areas {
				exempt := r.ReligionExempt && a.SubjectCode == domain.ReligionSubjectCode
				for _, comp := range a.Competencies {
					level := r.Final[comp.ID]
					if exempt {
						level = Exempt
					}
					_ = w.Write([]string{fmt.Sprint(i + 1), r.Student.Code, r.Student.Document, r.Student.Name, a.Name, comp.Name, level, r.Recovery[comp.ID], situation})
				}
			}
		}
	}
	w.Flush()
	return name, buf.Bytes(), w.Error()
}

func slugify(s string) string {
	r := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n", "°", "", ".", "", "·", "", ",", "")
	s = r.Replace(strings.ToLower(s))
	return strings.Join(strings.Fields(s), "-")
}
