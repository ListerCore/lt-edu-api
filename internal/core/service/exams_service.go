package service

import (
	"context"
	"encoding/csv"
	"errors"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// EventExamsChanged: {institution, area (bank | mock | sheets | admissions), mock}
const EventExamsChanged = "exams.changed"

var (
	examManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	// Teachers write the bank; secretaría loads the answer sheets
	examTeachers = map[string]bool{"teacher": true, "tutor": true}
	examLoaders  = map[string]bool{"secretary": true}
	letters      = []string{"A", "B", "C", "D", "E"}
)

// ExamsService runs the academies' evaluation (phase 8 of docs/plan-academico.md): the question bank per course,
// mock admission exams with the weight of each course per area, the answer sheets, scores, rankings, each
// student's progress and the students who got into university
type ExamsService struct {
	repo       port.ExamsRepository
	assessment *AssessmentService
}

func NewExamsService(repo port.ExamsRepository, assessment *AssessmentService) *ExamsService {
	return &ExamsService{repo: repo, assessment: assessment}
}

type examScope struct {
	attendanceScope
}

func (sc examScope) manager() bool { return examManagers[sc.role] }
func (sc examScope) teacher() bool { return sc.manager() || examTeachers[sc.role] }
func (sc examScope) loader() bool  { return sc.manager() || examLoaders[sc.role] }
func (sc examScope) reader() bool  { return sc.teacher() || sc.loader() }

func (s *ExamsService) scope(ctx context.Context, token, slug string) (examScope, error) {
	gsc, err := s.assessment.scope(ctx, token, slug)
	if errors.Is(err, domain.ErrGradesForbidden) {
		return examScope{}, domain.ErrExamsForbidden
	}
	if err != nil {
		return examScope{}, err
	}
	if gsc.institution.Kind != domain.KindCEPRE {
		return examScope{}, domain.ErrNotAnAcademy
	}
	sc := examScope{gsc.attendanceScope}
	if !sc.reader() && !sc.family() {
		return sc, domain.ErrExamsForbidden
	}
	return sc, nil
}

func (s *ExamsService) changed(ctx context.Context, sc examScope, area, mockID string) {
	s.assessment.academic.live.Notify(ctx, EventExamsChanged, map[string]string{"institution": sc.institution.Slug, "area": area, "mock": mockID})
}

func (s *ExamsService) name(sc examScope) string {
	if sc.userName != "" {
		return sc.userName
	}
	return sc.email
}

// ---------- overview ----------

type ExamLevel struct {
	ID   string
	Code string
	Name string
}

type ExamsOverview struct {
	Periods                          []domain.Period
	Period                           domain.Period
	Levels                           []ExamLevel
	Courses                          []domain.Course
	Mocks                            []domain.MockExam
	CanManage, CanWriteBank, CanLoad bool
}

func (s *ExamsService) Overview(ctx context.Context, token, slug, periodID string) (ExamsOverview, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ExamsOverview{}, err
	}
	if sc.family() {
		return ExamsOverview{}, domain.ErrExamsForbidden
	}
	p, periods, err := s.assessment.period(ctx, gradeScope{attendanceScope: sc.attendanceScope}, periodID)
	if err != nil {
		return ExamsOverview{}, err
	}
	out := ExamsOverview{Periods: periods, Period: p, CanManage: sc.manager(), CanWriteBank: sc.teacher(), CanLoad: sc.loader()}
	levels, err := s.assessment.academic.repo.Levels(ctx, sc.institution.ID)
	if err != nil {
		return out, err
	}
	for _, l := range levels {
		out.Levels = append(out.Levels, ExamLevel{ID: l.ID, Code: l.Code, Name: l.Name})
	}
	if out.Courses, err = s.repo.Courses(ctx, sc.institution.ID); err != nil {
		return out, err
	}
	if p.ID != "" {
		if out.Mocks, err = s.repo.MockExams(ctx, sc.institution.ID, p.ID); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ---------- question bank ----------

type QuestionPage struct {
	Items []domain.Question
	Total int
}

func (s *ExamsService) Questions(ctx context.Context, token, slug string, f domain.QuestionFilter) (QuestionPage, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return QuestionPage{}, err
	}
	if !sc.teacher() {
		return QuestionPage{}, domain.ErrExamsForbidden
	}
	f.InstitutionID = sc.institution.ID
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	f.Query = strings.TrimSpace(f.Query)
	items, total, err := s.repo.Questions(ctx, f)
	return QuestionPage{Items: items, Total: total}, err
}

func (s *ExamsService) SaveQuestion(ctx context.Context, token, slug string, q domain.Question) (domain.Question, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Question{}, err
	}
	if !sc.teacher() {
		return domain.Question{}, domain.ErrExamsForbidden
	}
	if q.ID != "" {
		cur, err := s.repo.Question(ctx, sc.institution.ID, q.ID)
		if err != nil {
			return domain.Question{}, err
		}
		if !sc.manager() && cur.CreatedBy != sc.userID {
			return domain.Question{}, domain.ErrNotYourQuestion
		}
	}
	courses, err := s.repo.Courses(ctx, sc.institution.ID)
	if err != nil {
		return domain.Question{}, err
	}
	fields := map[string]string{}
	if !slices.ContainsFunc(courses, func(c domain.Course) bool { return c.Code == q.CourseCode }) {
		fields["courseCode"] = "Elige un curso del plan."
	}
	q.Stem, q.Topic, q.Explanation = strings.TrimSpace(q.Stem), strings.TrimSpace(q.Topic), strings.TrimSpace(q.Explanation)
	switch n := len([]rune(q.Stem)); {
	case n == 0:
		fields["stem"] = "Escribe el enunciado."
	case n > 3000:
		fields["stem"] = "El enunciado tiene hasta 3000 caracteres."
	}
	if len(q.Options) != 5 {
		fields["options"] = "Una pregunta tiene cinco alternativas (A a E)."
	} else {
		seen := map[string]bool{}
		for i := range q.Options {
			q.Options[i] = strings.TrimSpace(q.Options[i])
			if q.Options[i] == "" {
				fields["options"] = "Completa las cinco alternativas."
			} else if seen[strings.ToLower(q.Options[i])] {
				fields["options"] = "Hay alternativas repetidas."
			}
			seen[strings.ToLower(q.Options[i])] = true
		}
	}
	if !slices.Contains(letters, q.Answer) {
		fields["answer"] = "Marca la alternativa correcta."
	}
	if !slices.Contains([]string{"easy", "medium", "hard"}, q.Difficulty) {
		fields["difficulty"] = "Elige la dificultad."
	}
	if len([]rune(q.Topic)) > 160 || len([]rune(q.Explanation)) > 3000 {
		fields["explanation"] = "El tema o la solución son demasiado largos."
	}
	if len(fields) > 0 {
		return domain.Question{}, domain.Validation(fields)
	}
	q.CreatedBy, q.AuthorName = sc.userID, s.name(sc)
	out, err := s.repo.SaveQuestion(ctx, sc.institution.ID, q)
	if err == nil {
		s.changed(ctx, sc, "bank", "")
	}
	return out, err
}

func (s *ExamsService) DeleteQuestion(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	cur, err := s.repo.Question(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if !sc.manager() && (!sc.teacher() || cur.CreatedBy != sc.userID) {
		return domain.ErrNotYourQuestion
	}
	if err := s.repo.DeleteQuestion(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "bank", "")
	return nil
}

// ---------- mock exams ----------

type MockDetail struct {
	Mock     domain.MockExam
	Key      []domain.KeyItem
	CanEdit  bool
	Students int
}

func (s *ExamsService) managerScope(ctx context.Context, token, slug string) (examScope, error) {
	sc, err := s.scope(ctx, token, slug)
	if err == nil && !sc.manager() {
		return sc, domain.ErrExamsReadOnly
	}
	return sc, err
}

func (s *ExamsService) Mock(ctx context.Context, token, slug, id string) (MockDetail, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return MockDetail{}, err
	}
	if sc.family() {
		return MockDetail{}, domain.ErrExamsForbidden
	}
	return s.mock(ctx, sc, id)
}

func (s *ExamsService) mock(ctx context.Context, sc examScope, id string) (MockDetail, error) {
	m, err := s.repo.MockExam(ctx, sc.institution.ID, id)
	if err != nil {
		return MockDetail{}, err
	}
	key, err := s.repo.Key(ctx, id)
	if err != nil {
		return MockDetail{}, err
	}
	students, err := s.repo.PeriodStudents(ctx, sc.institution.ID, m.PeriodID)
	if err != nil {
		return MockDetail{}, err
	}
	n := 0
	levels := mockLevels(m)
	for _, st := range students {
		if len(levels) == 0 || levels[st.LevelID] {
			n++
		}
	}
	return MockDetail{Mock: m, Key: key, CanEdit: sc.manager(), Students: n}, nil
}

// mockLevels are the areas that take the exam (those with weights)
func mockLevels(m domain.MockExam) map[string]bool {
	out := map[string]bool{}
	for _, w := range m.Weights {
		out[w.LevelID] = true
	}
	return out
}

func (s *ExamsService) SaveMock(ctx context.Context, token, slug string, m domain.MockExam) (domain.MockExam, error) {
	sc, err := s.managerScope(ctx, token, slug)
	if err != nil {
		return domain.MockExam{}, err
	}
	fields := map[string]string{}
	m.Name, m.Notes = strings.TrimSpace(m.Name), strings.TrimSpace(m.Notes)
	if m.Name == "" || len([]rune(m.Name)) > 120 {
		fields["name"] = "Ponle un nombre de hasta 120 caracteres."
	}
	if m.HeldOn.IsZero() {
		fields["heldOn"] = "Indica la fecha."
	}
	if m.Duration < 10 || m.Duration > 600 {
		fields["duration"] = "La duración va de 10 a 600 minutos."
	}
	if m.PointsRight <= 0 || m.PointsRight > 100 {
		fields["pointsRight"] = "Los puntos por acierto van de 0 a 100."
	}
	if m.PointsWrong < 0 || m.PointsWrong > 100 {
		fields["pointsWrong"] = "El descuento por error va de 0 a 100."
	}
	if m.ID == "" {
		p, err := s.assessment.academic.repo.Period(ctx, sc.institution.ID, m.PeriodID)
		if err != nil {
			return domain.MockExam{}, err
		}
		if !m.HeldOn.IsZero() && (m.HeldOn.Before(p.StartsOn) || m.HeldOn.After(p.EndsOn)) {
			fields["heldOn"] = "La fecha debe estar dentro del ciclo."
		}
	}
	if len(fields) > 0 {
		return domain.MockExam{}, domain.Validation(fields)
	}
	out, err := s.repo.SaveMockExam(ctx, sc.institution.ID, m)
	if err == nil {
		s.changed(ctx, sc, "mock", out.ID)
	}
	return out, err
}

func (s *ExamsService) DeleteMock(ctx context.Context, token, slug, id string) error {
	sc, err := s.managerScope(ctx, token, slug)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteMockExam(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "mock", id)
	return nil
}

// StructureTemplate suggests the courses, questions and weights of a mock exam for some areas, from the weekly
// hours of each course in each area's plan: more hours, more questions and more weight
func (s *ExamsService) StructureTemplate(ctx context.Context, token, slug string, levelIDs []string, total int) ([]domain.MockCourse, []domain.MockWeight, error) {
	sc, err := s.managerScope(ctx, token, slug)
	if err != nil {
		return nil, nil, err
	}
	if total < 10 || total > 200 {
		return nil, nil, domain.Validation(map[string]string{"total": "El simulacro tiene de 10 a 200 preguntas."})
	}
	if len(levelIDs) == 0 {
		return nil, nil, domain.Validation(map[string]string{"levels": "Elige al menos un área."})
	}
	courses, err := s.repo.Courses(ctx, sc.institution.ID)
	if err != nil {
		return nil, nil, err
	}
	c, w := structureFromHours(courses, levelIDs, total)
	return c, w, nil
}

func structureFromHours(courses []domain.Course, levelIDs []string, total int) ([]domain.MockCourse, []domain.MockWeight) {
	type pick struct {
		course domain.Course
		avg    float64
	}
	var picks []pick
	sum := 0.0
	for _, c := range courses {
		h, n := 0, 0
		for _, l := range levelIDs {
			if v := c.Hours[l]; v > 0 {
				h += v
				n++
			}
		}
		if n == 0 {
			continue
		}
		avg := float64(h) / float64(len(levelIDs))
		picks = append(picks, pick{c, avg})
		sum += avg
	}
	var out []domain.MockCourse
	assigned := 0
	for i, p := range picks {
		q := max(1, int(math.Round(float64(total)*p.avg/sum)))
		out = append(out, domain.MockCourse{Code: p.course.Code, Name: p.course.Name, Questions: q, Position: i})
		assigned += q
	}
	// Adjust rounding on the largest courses so the total matches
	for assigned != total && len(out) > 0 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Questions > out[j].Questions })
		if assigned > total && out[0].Questions > 1 {
			out[0].Questions--
			assigned--
		} else if assigned < total {
			out[0].Questions++
			assigned++
		} else {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	var weights []domain.MockWeight
	for _, p := range picks {
		for _, l := range levelIDs {
			wt := 0.0
			if h := p.course.Hours[l]; h > 0 && p.avg > 0 {
				wt = math.Round(float64(h)/p.avg*4) / 4
			}
			weights = append(weights, domain.MockWeight{LevelID: l, CourseCode: p.course.Code, Weight: wt})
		}
	}
	return out, weights
}

func (s *ExamsService) SetStructure(ctx context.Context, token, slug, id string, courses []domain.MockCourse, weights []domain.MockWeight) (MockDetail, error) {
	sc, err := s.managerScope(ctx, token, slug)
	if err != nil {
		return MockDetail{}, err
	}
	m, err := s.repo.MockExam(ctx, sc.institution.ID, id)
	if err != nil {
		return MockDetail{}, err
	}
	if m.Sheets > 0 {
		return MockDetail{}, domain.ErrExamHasSheets
	}
	known, err := s.repo.Courses(ctx, sc.institution.ID)
	if err != nil {
		return MockDetail{}, err
	}
	names := map[string]string{}
	for _, c := range known {
		names[c.Code] = c.Name
	}
	levels, err := s.assessment.academic.repo.Levels(ctx, sc.institution.ID)
	if err != nil {
		return MockDetail{}, err
	}
	levelIDs := map[string]bool{}
	for _, l := range levels {
		levelIDs[l.ID] = true
	}
	total := 0
	seen := map[string]bool{}
	for i := range courses {
		c := &courses[i]
		if names[c.Code] == "" || seen[c.Code] {
			return MockDetail{}, domain.Validation(map[string]string{"courses": "Hay cursos que no son del plan o están repetidos."})
		}
		seen[c.Code] = true
		c.Name = names[c.Code]
		if c.Questions < 1 || c.Questions > 100 {
			return MockDetail{}, domain.Validation(map[string]string{"courses": "Cada curso tiene de 1 a 100 preguntas."})
		}
		total += c.Questions
	}
	if total == 0 || total > 300 {
		return MockDetail{}, domain.Validation(map[string]string{"courses": "El simulacro tiene de 1 a 300 preguntas."})
	}
	areas := map[string]bool{}
	for _, w := range weights {
		if !levelIDs[w.LevelID] || !seen[w.CourseCode] || w.Weight < 0 || w.Weight > 10 {
			return MockDetail{}, domain.Validation(map[string]string{"weights": "Los pesos van de 0 a 10 por área y curso del simulacro."})
		}
		areas[w.LevelID] = true
	}
	if len(areas) == 0 {
		return MockDetail{}, domain.Validation(map[string]string{"weights": "Elige al menos un área que rinda el simulacro."})
	}
	if err := s.repo.SetStructure(ctx, id, courses, weights); err != nil {
		return MockDetail{}, err
	}
	s.changed(ctx, sc, "mock", id)
	return s.mock(ctx, sc, id)
}

// FillFromBank gives every question number without one a random question of its course from the bank
func (s *ExamsService) FillFromBank(ctx context.Context, token, slug, id string) (MockDetail, int, error) {
	sc, err := s.managerScope(ctx, token, slug)
	if err != nil {
		return MockDetail{}, 0, err
	}
	m, err := s.repo.MockExam(ctx, sc.institution.ID, id)
	if err != nil {
		return MockDetail{}, 0, err
	}
	if m.Sheets > 0 {
		return MockDetail{}, 0, domain.ErrExamHasSheets
	}
	key, err := s.repo.Key(ctx, id)
	if err != nil {
		return MockDetail{}, 0, err
	}
	used := []string{}
	missing := map[string][]int{}
	for i, k := range key {
		if k.QuestionID != "" {
			used = append(used, k.QuestionID)
		} else {
			missing[k.CourseCode] = append(missing[k.CourseCode], i)
		}
	}
	var changed []domain.KeyItem
	for course, idx := range missing {
		qs, err := s.repo.PickQuestions(ctx, sc.institution.ID, course, len(idx), used)
		if err != nil {
			return MockDetail{}, 0, err
		}
		for j, q := range qs {
			k := key[idx[j]]
			k.QuestionID, k.Answer = q.ID, q.Answer
			changed = append(changed, k)
		}
	}
	if len(changed) > 0 {
		if err := s.repo.SaveKey(ctx, id, changed); err != nil {
			return MockDetail{}, 0, err
		}
		s.changed(ctx, sc, "mock", id)
	}
	d, err := s.mock(ctx, sc, id)
	return d, len(changed), err
}

// SaveKey sets the right option of question numbers (typed, or clearing a bank question)
func (s *ExamsService) SaveKey(ctx context.Context, token, slug, id string, items []domain.KeyItem) (MockDetail, error) {
	sc, err := s.managerScope(ctx, token, slug)
	if err != nil {
		return MockDetail{}, err
	}
	key, err := s.repo.Key(ctx, id)
	if err != nil {
		return MockDetail{}, err
	}
	if len(key) == 0 {
		return MockDetail{}, domain.ErrNotFound
	}
	byNumber := map[int]domain.KeyItem{}
	for _, k := range key {
		byNumber[k.Number] = k
	}
	var out []domain.KeyItem
	for _, it := range items {
		cur, ok := byNumber[it.Number]
		if !ok || (it.Answer != "" && !slices.Contains(letters, it.Answer)) {
			return MockDetail{}, domain.Validation(map[string]string{"key": "Cada respuesta es A, B, C, D o E."})
		}
		cur.Answer = it.Answer
		if it.QuestionID == "" {
			cur.QuestionID = ""
		}
		out = append(out, cur)
	}
	if err := s.repo.SaveKey(ctx, id, out); err != nil {
		return MockDetail{}, err
	}
	// Changing the key re-grades the sheets already loaded
	if err := s.regrade(ctx, sc, id); err != nil {
		return MockDetail{}, err
	}
	s.changed(ctx, sc, "mock", id)
	return s.mock(ctx, sc, id)
}

func (s *ExamsService) Publish(ctx context.Context, token, slug, id string, published bool) (MockDetail, error) {
	sc, err := s.managerScope(ctx, token, slug)
	if err != nil {
		return MockDetail{}, err
	}
	m, err := s.repo.MockExam(ctx, sc.institution.ID, id)
	if err != nil {
		return MockDetail{}, err
	}
	status := "draft"
	if published {
		if m.Questions == 0 || m.Keyed < m.Questions {
			return MockDetail{}, domain.ErrKeyIncomplete
		}
		status = "published"
	}
	if err := s.repo.SetStatus(ctx, sc.institution.ID, id, status); err != nil {
		return MockDetail{}, err
	}
	s.changed(ctx, sc, "mock", id)
	return s.mock(ctx, sc, id)
}

// Booklet is the printable exam: the questions in order with their options (bank questions), for staff
type BookletItem struct {
	Number   int
	Course   string
	Question *domain.Question
	Answer   string
}

func (s *ExamsService) Booklet(ctx context.Context, token, slug, id string) (domain.MockExam, []BookletItem, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.MockExam{}, nil, err
	}
	if !sc.teacher() {
		return domain.MockExam{}, nil, domain.ErrExamsForbidden
	}
	d, err := s.mock(ctx, sc, id)
	if err != nil {
		return domain.MockExam{}, nil, err
	}
	var ids []string
	for _, k := range d.Key {
		if k.QuestionID != "" {
			ids = append(ids, k.QuestionID)
		}
	}
	qs, err := s.repo.QuestionsByID(ctx, sc.institution.ID, ids)
	if err != nil {
		return d.Mock, nil, err
	}
	byID := map[string]domain.Question{}
	for _, q := range qs {
		byID[q.ID] = q
	}
	names := map[string]string{}
	for _, c := range d.Mock.Courses {
		names[c.Code] = c.Name
	}
	out := make([]BookletItem, len(d.Key))
	for i, k := range d.Key {
		out[i] = BookletItem{Number: k.Number, Course: names[k.CourseCode], Answer: k.Answer}
		if q, ok := byID[k.QuestionID]; ok {
			out[i].Question = &q
		}
	}
	return d.Mock, out, nil
}

// ---------- answer sheets ----------

type SheetEntry struct {
	// A student id, or a code / DNI read from a file
	StudentID string
	Code      string
	Answers   string
}

type LoadReport struct {
	Saved   int
	Unknown []string
	// Students of the cycle in areas that do not take this exam
	OtherArea []string
}

func (s *ExamsService) weightsFor(m domain.MockExam) map[string]map[string]float64 {
	out := map[string]map[string]float64{}
	for _, w := range m.Weights {
		if out[w.LevelID] == nil {
			out[w.LevelID] = map[string]float64{}
		}
		out[w.LevelID][w.CourseCode] = w.Weight
	}
	return out
}

func (s *ExamsService) LoadSheets(ctx context.Context, token, slug, id, source string, entries []SheetEntry) (LoadReport, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return LoadReport{}, err
	}
	if !sc.loader() {
		return LoadReport{}, domain.ErrExamsReadOnly
	}
	m, err := s.repo.MockExam(ctx, sc.institution.ID, id)
	if err != nil {
		return LoadReport{}, err
	}
	if m.Questions == 0 || m.Keyed < m.Questions {
		return LoadReport{}, domain.ErrKeyIncomplete
	}
	if source != "reader" {
		source = "manual"
	}
	key, err := s.repo.Key(ctx, id)
	if err != nil {
		return LoadReport{}, err
	}
	students, err := s.repo.PeriodStudents(ctx, sc.institution.ID, m.PeriodID)
	if err != nil {
		return LoadReport{}, err
	}
	byID, byCode := map[string]domain.ExamStudent{}, map[string]domain.ExamStudent{}
	for _, st := range students {
		byID[st.ID] = st
		byCode[strings.ToUpper(st.Code)] = st
		byCode[st.Document] = st
	}
	levels := mockLevels(m)
	weights := s.weightsFor(m)
	report := LoadReport{}
	var sheets []domain.AnswerSheet
	for _, e := range entries {
		st, ok := byID[e.StudentID]
		if !ok {
			st, ok = byCode[strings.ToUpper(strings.TrimSpace(e.Code))]
		}
		if !ok {
			report.Unknown = append(report.Unknown, strings.TrimSpace(e.Code+e.StudentID))
			continue
		}
		if len(levels) > 0 && !levels[st.LevelID] {
			report.OtherArea = append(report.OtherArea, st.Name)
			continue
		}
		answers := NormalizeAnswers(e.Answers, m.Questions)
		total, by := ScoreSheet(answers, key, weights[st.LevelID], m.PointsRight, m.PointsWrong)
		sheets = append(sheets, domain.AnswerSheet{StudentID: st.ID, SectionID: st.SectionID, LevelID: st.LevelID, Answers: answers, Source: source,
			Score: total.Score, Right: total.Right, Wrong: total.Wrong, Blank: total.Blank, ByCourse: by})
	}
	if len(sheets) > 0 {
		if err := s.repo.SaveSheets(ctx, id, sheets); err != nil {
			return report, err
		}
		s.changed(ctx, sc, "sheets", id)
	}
	report.Saved = len(sheets)
	return report, nil
}

// ParseReaderFile reads the export of an optical reader or a spreadsheet: one line per student with the code or DNI
// first and the answers after it (all together, or one per column). A header line is skipped
func ParseReaderFile(text string) ([]SheetEntry, error) {
	text = strings.TrimPrefix(text, "\xEF\xBB\xBF")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	sep := ','
	for _, l := range lines {
		if strings.Count(l, ";") > strings.Count(l, ",") {
			sep = ';'
		} else if strings.Count(l, "\t") > strings.Count(l, ",") {
			sep = '\t'
		}
		if strings.TrimSpace(l) != "" {
			break
		}
	}
	r := csv.NewReader(strings.NewReader(strings.Join(lines, "\n")))
	r.Comma = sep
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, domain.Validation(map[string]string{"file": "No pudimos leer el archivo: debe ser CSV (código y respuestas por fila)."})
	}
	var out []SheetEntry
	for i, rec := range records {
		if len(rec) < 2 {
			continue
		}
		code := strings.TrimSpace(rec[0])
		if i == 0 && !strings.ContainsAny(code, "0123456789") {
			continue
		}
		answers := strings.Join(rec[1:], "")
		if len(rec) > 2 {
			// One column per question: empty cells are blanks
			var b strings.Builder
			for _, c := range rec[1:] {
				c = strings.TrimSpace(c)
				if c == "" {
					b.WriteByte('-')
				} else {
					b.WriteString(c[:1])
				}
			}
			answers = b.String()
		}
		out = append(out, SheetEntry{Code: code, Answers: answers})
	}
	if len(out) == 0 {
		return nil, domain.Validation(map[string]string{"file": "El archivo no tiene filas con código y respuestas."})
	}
	return out, nil
}

func (s *ExamsService) LoadFile(ctx context.Context, token, slug, id, text string) (LoadReport, error) {
	if len(text) > 2<<20 {
		return LoadReport{}, domain.Validation(map[string]string{"file": "El archivo pesa más de 2 MB."})
	}
	entries, err := ParseReaderFile(text)
	if err != nil {
		return LoadReport{}, err
	}
	return s.LoadSheets(ctx, token, slug, id, "reader", entries)
}

func (s *ExamsService) DeleteSheet(ctx context.Context, token, slug, id, studentID string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	if !sc.loader() {
		return domain.ErrExamsReadOnly
	}
	if _, err := s.repo.MockExam(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	if err := s.repo.DeleteSheet(ctx, id, studentID); err != nil {
		return err
	}
	s.changed(ctx, sc, "sheets", id)
	return nil
}

func (s *ExamsService) regrade(ctx context.Context, sc examScope, id string) error {
	m, err := s.repo.MockExam(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	sheets, err := s.repo.Sheets(ctx, id)
	if err != nil || len(sheets) == 0 {
		return err
	}
	key, err := s.repo.Key(ctx, id)
	if err != nil {
		return err
	}
	weights := s.weightsFor(m)
	for i, a := range sheets {
		total, by := ScoreSheet(a.Answers, key, weights[a.LevelID], m.PointsRight, m.PointsWrong)
		sheets[i].Score, sheets[i].Right, sheets[i].Wrong, sheets[i].Blank, sheets[i].ByCourse = total.Score, total.Right, total.Wrong, total.Blank, by
	}
	return s.repo.SaveSheets(ctx, id, sheets)
}

// ---------- results ----------

type CourseStat struct {
	Code    string
	Name    string
	Average float64
	Right   float64
}

type MockResults struct {
	Mock    domain.MockExam
	Sheets  []domain.AnswerSheet
	Courses []CourseStat
	Missing []domain.ExamStudent
	Average float64
	Best    float64
}

func (s *ExamsService) Results(ctx context.Context, token, slug, id string) (MockResults, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return MockResults{}, err
	}
	if sc.family() {
		return MockResults{}, domain.ErrExamsForbidden
	}
	m, err := s.repo.MockExam(ctx, sc.institution.ID, id)
	if err != nil {
		return MockResults{}, err
	}
	sheets, err := s.repo.Sheets(ctx, id)
	if err != nil {
		return MockResults{}, err
	}
	RankSheets(sheets)
	out := MockResults{Mock: m, Sheets: sheets}
	taken := map[string]bool{}
	for _, a := range sheets {
		taken[a.StudentID] = true
		out.Average += a.Score
		out.Best = max(out.Best, a.Score)
	}
	if len(sheets) > 0 {
		out.Average = round3(out.Average / float64(len(sheets)))
	}
	for _, c := range m.Courses {
		st := CourseStat{Code: c.Code, Name: c.Name}
		for _, a := range sheets {
			st.Average += a.ByCourse[c.Code].Score
			st.Right += float64(a.ByCourse[c.Code].Right)
		}
		if len(sheets) > 0 {
			st.Average = round3(st.Average / float64(len(sheets)))
			st.Right = math.Round(st.Right/float64(len(sheets))*10) / 10
		}
		out.Courses = append(out.Courses, st)
	}
	students, err := s.repo.PeriodStudents(ctx, sc.institution.ID, m.PeriodID)
	if err != nil {
		return out, err
	}
	levels := mockLevels(m)
	for _, st := range students {
		if !taken[st.ID] && (len(levels) == 0 || levels[st.LevelID]) {
			out.Missing = append(out.Missing, st)
		}
	}
	return out, nil
}

// StudentMock is one mock exam in a student's history, with the places reached
type StudentMock struct {
	Mock    domain.MockExam
	Sheet   domain.AnswerSheet
	Takers  int
	InLevel int
}

type StudentExams struct {
	Student    domain.CardHolder
	Mocks      []StudentMock
	Admissions []domain.Admission
}

func (s *ExamsService) history(ctx context.Context, sc examScope, h domain.CardHolder, all bool) (StudentExams, error) {
	sheets, mocks, err := s.repo.StudentSheets(ctx, sc.institution.ID, h.ID, all)
	if err != nil {
		return StudentExams{}, err
	}
	out := StudentExams{Student: h}
	for _, m := range mocks {
		var mine []domain.AnswerSheet
		for _, a := range sheets {
			if a.MockExamID == m.ID {
				mine = append(mine, a)
			}
		}
		RankSheets(mine)
		for _, a := range mine {
			if a.StudentID != h.ID {
				continue
			}
			inLevel := 0
			for _, b := range mine {
				if b.LevelID == a.LevelID {
					inLevel++
				}
			}
			if h.Name == "" {
				out.Student.Name = a.StudentName
			}
			out.Mocks = append(out.Mocks, StudentMock{Mock: m, Sheet: a, Takers: len(mine), InLevel: inLevel})
		}
	}
	admissions, err := s.repo.Admissions(ctx, sc.institution.ID)
	if err != nil {
		return out, err
	}
	for _, a := range admissions {
		if a.StudentID == h.ID {
			out.Admissions = append(out.Admissions, a)
		}
	}
	return out, nil
}

func (s *ExamsService) StudentHistory(ctx context.Context, token, slug, studentID string) (StudentExams, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return StudentExams{}, err
	}
	if sc.family() {
		mine, err := s.assessment.attendance.ownStudents(ctx, sc.attendanceScope)
		if err != nil {
			return StudentExams{}, err
		}
		i := slices.IndexFunc(mine, func(h domain.CardHolder) bool { return h.ID == studentID })
		if i < 0 {
			return StudentExams{}, domain.ErrNotYourStudent
		}
		return s.history(ctx, sc, mine[i], false)
	}
	return s.history(ctx, sc, domain.CardHolder{Kind: "student", ID: studentID}, true)
}

// Mine is the family view: each child (or the student) with their published mock exams
func (s *ExamsService) Mine(ctx context.Context, token, slug string) ([]StudentExams, error) {
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
	var out []StudentExams
	for _, h := range mine {
		x, err := s.history(ctx, sc, h, false)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// ---------- admissions ----------

func (s *ExamsService) Admissions(ctx context.Context, token, slug string) ([]domain.Admission, bool, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, false, err
	}
	if sc.family() {
		return nil, false, domain.ErrExamsForbidden
	}
	list, err := s.repo.Admissions(ctx, sc.institution.ID)
	return list, sc.manager(), err
}

func (s *ExamsService) SaveAdmission(ctx context.Context, token, slug string, a domain.Admission) (domain.Admission, error) {
	sc, err := s.managerScope(ctx, token, slug)
	if err != nil {
		return domain.Admission{}, err
	}
	a.University, a.Career, a.Exam = strings.TrimSpace(a.University), strings.TrimSpace(a.Career), strings.TrimSpace(a.Exam)
	fields := map[string]string{}
	if a.StudentID == "" && a.ID == "" {
		fields["studentId"] = "Elige al alumno."
	}
	if a.University == "" || len([]rune(a.University)) > 120 {
		fields["university"] = "Indica la universidad."
	}
	if a.Career == "" || len([]rune(a.Career)) > 160 {
		fields["career"] = "Indica la carrera."
	}
	if a.Exam == "" || len([]rune(a.Exam)) > 120 {
		fields["exam"] = "Indica el examen (por ejemplo, Admisión 2026-II)."
	}
	if a.AdmittedOn.IsZero() || a.AdmittedOn.After(time.Now().AddDate(0, 0, 1)) {
		fields["admittedOn"] = "Indica la fecha del examen (no futura)."
	}
	if a.Place != nil && *a.Place < 1 {
		fields["place"] = "El puesto empieza en 1."
	}
	if len(fields) > 0 {
		return domain.Admission{}, domain.Validation(fields)
	}
	out, err := s.repo.SaveAdmission(ctx, sc.institution.ID, a)
	if errors.Is(err, domain.ErrNotFound) && a.ID == "" {
		return out, domain.Validation(map[string]string{"studentId": "El alumno no es de la academia."})
	}
	if err == nil {
		s.changed(ctx, sc, "admissions", "")
	}
	return out, err
}

func (s *ExamsService) DeleteAdmission(ctx context.Context, token, slug, id string) error {
	sc, err := s.managerScope(ctx, token, slug)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteAdmission(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "admissions", "")
	return nil
}

// UserID is the signed-in user (to mark their own questions); empty when the session is not valid
func (s *ExamsService) UserID(ctx context.Context, token string) string {
	me, err := s.assessment.academic.auth.Me(ctx, token)
	if err != nil {
		return ""
	}
	return me.User.ID
}
