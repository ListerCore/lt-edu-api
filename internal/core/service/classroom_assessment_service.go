package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
)

const (
	maxQuestions      = 60
	maxOptions        = 8
	maxAnswerLength   = 5000
	maxAcceptedAnswer = 10
)

// SetGrades lets the classroom take graded levels to the official record through the assessment service, so every
// rule of the record applies (open term, the teacher's own sheet, a C needs its conclusion)
func (s *ClassroomService) SetGrades(g *AssessmentService) { s.grades = g }

// AssessmentView is a quiz or task as the person sees it. Staff get the right answers, the roster and the
// competencies; a student gets their attempts and whether they may start one
type AssessmentView struct {
	Assessment   domain.Assessment
	CanEdit      bool
	Family       bool
	Competencies []domain.CompetencyOption
	// Staff
	Roster        []domain.QuizRosterRow
	QuestionsLock bool
	// Student
	Attempts  []domain.QuizSubmission
	Extension *time.Time
	OpenNow   bool
	CanStart  bool
}

// assessmentAccess loads the assessment and the room access of the person
func (s *ClassroomService) assessmentAccess(ctx context.Context, token, slug, itemID string) (classroomScope, domain.Assessment, bool, string, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, domain.Assessment{}, false, "", err
	}
	a, err := s.repo.Assessment(ctx, sc.institution.ID, itemID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return sc, a, false, "", domain.ErrNotAnAssessment
		}
		return sc, a, false, "", err
	}
	_, canEdit, viewer, err := s.roomAccess(ctx, sc, a.RoomID)
	return sc, a, canEdit, viewer, err
}

// hideAnswers strips the right answers a student must not see
func hideAnswers(a *domain.Assessment) {
	for i := range a.Questions {
		a.Questions[i].Correct, a.Questions[i].Accepted = nil, nil
	}
}

func (s *ClassroomService) Assessment(ctx context.Context, token, slug, itemID string) (AssessmentView, error) {
	sc, a, canEdit, viewer, err := s.assessmentAccess(ctx, token, slug, itemID)
	if err != nil {
		return AssessmentView{}, err
	}
	comps, err := s.repo.RoomCompetencies(ctx, sc.institution.ID, a.RoomID)
	if err != nil {
		return AssessmentView{}, err
	}
	view := AssessmentView{CanEdit: canEdit, Family: sc.family(), Competencies: comps}
	if !sc.family() {
		roster, err := s.repo.Roster(ctx, sc.institution.ID, itemID)
		if err != nil {
			return AssessmentView{}, err
		}
		handed, err := s.repo.HandedIn(ctx, itemID)
		if err != nil {
			return AssessmentView{}, err
		}
		view.Assessment, view.Roster, view.QuestionsLock = a, roster, handed > 0
		return view, nil
	}
	// Families: only a published item of an open unit, and a guardian only reads
	u, err := s.repo.Unit(ctx, sc.institution.ID, a.UnitID)
	if err != nil {
		return AssessmentView{}, err
	}
	if !a.Published || !u.OpenOn(s.now()) {
		return AssessmentView{}, domain.ErrNotFound
	}
	if viewer != "" {
		attempts, err := s.repo.Attempts(ctx, itemID, viewer)
		if err != nil {
			return AssessmentView{}, err
		}
		ext, err := s.repo.Extension(ctx, itemID, viewer)
		if err != nil {
			return AssessmentView{}, err
		}
		view.Attempts, view.Extension = attempts, ext
		view.OpenNow = a.Open(s.now(), ext)
		inProgress := len(attempts) > 0 && attempts[0].Status == domain.SubmissionInProgress
		view.CanStart = view.OpenNow && !inProgress && len(attempts) < a.MaxAttempts
	}
	// Answers stay hidden until the student's latest attempt is graded and the teacher shows them
	graded := len(view.Attempts) > 0 && view.Attempts[0].Status == domain.SubmissionGraded
	if !(a.ShowAnswers && graded) {
		hideAnswers(&a)
	}
	view.Assessment = a
	return view, nil
}

// validateQuestions trims and checks the questions of a quiz
func validateQuestions(qs []domain.QuizQuestion) map[string]string {
	fields := map[string]string{}
	if len(qs) > maxQuestions {
		fields["questions"] = "Hasta 60 preguntas por cuestionario."
		return fields
	}
	for i := range qs {
		q := &qs[i]
		key := "questions." + itoa(i)
		q.Prompt = strings.TrimSpace(q.Prompt)
		if q.Points == 0 {
			q.Points = 1
		}
		switch {
		case q.Prompt == "" || utf8.RuneCountInString(q.Prompt) > 3000:
			fields[key] = "Escribe la pregunta (hasta 3000 caracteres)."
		case q.Points < 1 || q.Points > 100:
			fields[key] = "Cada pregunta vale de 1 a 100 puntos."
		}
		switch q.Kind {
		case domain.QuestionSingle, domain.QuestionMultiple:
			var opts []string
			for _, o := range q.Options {
				if o = strings.TrimSpace(o); o != "" {
					opts = append(opts, o)
				}
			}
			q.Options, q.Accepted = opts, nil
			if len(opts) < 2 || len(opts) > maxOptions {
				fields[key] = "Escribe de 2 a 8 alternativas."
				continue
			}
			var correct []int
			for _, c := range q.Correct {
				if c >= 0 && c < len(opts) && !slices.Contains(correct, c) {
					correct = append(correct, c)
				}
			}
			q.Correct = correct
			if len(correct) == 0 || (q.Kind == domain.QuestionSingle && len(correct) != 1) {
				fields[key] = "Marca la alternativa correcta."
			}
		case domain.QuestionShort:
			var acc []string
			for _, a := range q.Accepted {
				if a = strings.TrimSpace(a); a != "" && len(acc) < maxAcceptedAnswer {
					acc = append(acc, a)
				}
			}
			q.Accepted, q.Options, q.Correct = acc, nil, nil
			if len(acc) == 0 {
				fields[key] = "Escribe al menos una respuesta aceptada."
			}
		case domain.QuestionOpen:
			q.Options, q.Correct, q.Accepted = nil, nil, nil
		default:
			fields[key] = "Elige el tipo de pregunta."
		}
	}
	return fields
}

// SaveAssessment sets the window, attempts and competencies and, for a quiz, the questions. Once someone handed it
// in, questions are locked (changing them would change grades already given); settings can still change
func (s *ClassroomService) SaveAssessment(ctx context.Context, token, slug string, in domain.Assessment, questions []domain.QuizQuestion, replaceQuestions bool) (domain.Assessment, error) {
	sc, current, canEdit, _, err := s.assessmentAccess(ctx, token, slug, in.ItemID)
	if err != nil {
		return domain.Assessment{}, err
	}
	if !canEdit {
		return domain.Assessment{}, domain.ErrClassroomReadOnly
	}
	fields := map[string]string{}
	if in.MaxAttempts < 1 || in.MaxAttempts > 10 {
		fields["maxAttempts"] = "De 1 a 10 intentos."
	}
	if in.OpensAt != nil && in.ClosesAt != nil && !in.ClosesAt.After(*in.OpensAt) {
		fields["closesAt"] = "El cierre debe ser después de la apertura."
	}
	comps, err := s.repo.RoomCompetencies(ctx, sc.institution.ID, current.RoomID)
	if err != nil {
		return domain.Assessment{}, err
	}
	in.CompetencyIDs = unique(in.CompetencyIDs)
	for _, id := range in.CompetencyIDs {
		if !slices.ContainsFunc(comps, func(c domain.CompetencyOption) bool { return c.ID == id }) {
			fields["competencyIds"] = "Elige competencias del área del aula."
		}
	}
	if !replaceQuestions || current.Kind != domain.ItemQuiz {
		questions = nil
	} else {
		handed, err := s.repo.HandedIn(ctx, in.ItemID)
		if err != nil {
			return domain.Assessment{}, err
		}
		if handed > 0 {
			return domain.Assessment{}, domain.ErrQuestionsLocked
		}
		if questions == nil {
			questions = []domain.QuizQuestion{}
		}
		for k, v := range validateQuestions(questions) {
			fields[k] = v
		}
	}
	if len(fields) > 0 {
		return domain.Assessment{}, domain.Validation(fields)
	}
	out, err := s.repo.SaveAssessment(ctx, sc.institution.ID, in, questions)
	if err != nil {
		return domain.Assessment{}, err
	}
	s.changed(ctx, sc, "room", current.RoomID)
	return out, nil
}

// StartAttempt opens a new attempt for the student, within the window and the attempts allowed
func (s *ClassroomService) StartAttempt(ctx context.Context, token, slug, itemID string) (domain.QuizSubmission, error) {
	view, err := s.Assessment(ctx, token, slug, itemID)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	if sc.role != "student" {
		return domain.QuizSubmission{}, domain.ErrClassroomForbidden
	}
	if len(view.Attempts) > 0 && view.Attempts[0].Status == domain.SubmissionInProgress {
		return view.Attempts[0], nil
	}
	if !view.OpenNow {
		return domain.QuizSubmission{}, domain.ErrAssessmentClosed
	}
	if len(view.Attempts) >= view.Assessment.MaxAttempts {
		return domain.QuizSubmission{}, domain.ErrNoAttemptsLeft
	}
	_, _, _, viewer, err := s.assessmentAccess(ctx, token, slug, itemID)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	out, err := s.repo.CreateAttempt(ctx, itemID, viewer, len(view.Attempts)+1)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	// Starting an assessment is participation too
	_ = s.repo.RecordView(ctx, itemID, viewer, s.now())
	return out, nil
}

// ownAttempt loads an attempt of the signed-in student
func (s *ClassroomService) ownAttempt(ctx context.Context, token, slug, submissionID string) (classroomScope, domain.QuizSubmission, domain.Assessment, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, domain.QuizSubmission{}, domain.Assessment{}, err
	}
	if sc.role != "student" {
		return sc, domain.QuizSubmission{}, domain.Assessment{}, domain.ErrClassroomForbidden
	}
	sub, err := s.repo.QuizSubmission(ctx, sc.institution.ID, submissionID)
	if err != nil {
		return sc, sub, domain.Assessment{}, err
	}
	_, a, _, viewer, err := s.assessmentAccess(ctx, token, slug, sub.ItemID)
	if err != nil {
		return sc, sub, a, err
	}
	if viewer == "" || viewer != sub.StudentID {
		return sc, sub, a, domain.ErrSubmissionNotYours
	}
	return sc, sub, a, nil
}

// cleanAnswers keeps answers to the assessment's own questions, within range
func cleanAnswers(a domain.Assessment, answers map[string]domain.QuizAnswer) map[string]domain.QuizAnswer {
	out := map[string]domain.QuizAnswer{}
	for _, q := range a.Questions {
		ans, ok := answers[q.ID]
		if !ok {
			continue
		}
		var choices []int
		for _, c := range ans.Choices {
			if c >= 0 && c < len(q.Options) && !slices.Contains(choices, c) {
				choices = append(choices, c)
			}
		}
		text := strings.TrimSpace(ans.Text)
		if utf8.RuneCountInString(text) > maxAnswerLength {
			text = string([]rune(text)[:maxAnswerLength])
		}
		switch q.Kind {
		case domain.QuestionSingle:
			if len(choices) > 1 {
				choices = choices[:1]
			}
			out[q.ID] = domain.QuizAnswer{Choices: choices}
		case domain.QuestionMultiple:
			out[q.ID] = domain.QuizAnswer{Choices: choices}
		default:
			out[q.ID] = domain.QuizAnswer{Text: text}
		}
	}
	return out
}

// SaveAnswers keeps the answers of an attempt in progress (the page saves as the student goes)
func (s *ClassroomService) SaveAnswers(ctx context.Context, token, slug, submissionID string, answers map[string]domain.QuizAnswer) error {
	_, sub, a, err := s.ownAttempt(ctx, token, slug, submissionID)
	if err != nil {
		return err
	}
	if sub.Status != domain.SubmissionInProgress {
		return domain.ErrAttemptFinished
	}
	return s.repo.SaveAnswers(ctx, submissionID, cleanAnswers(a, answers))
}

// HandIn closes the attempt: the machine grades what it can and the teacher is told. Handing in after the deadline
// (and the student's extension) is kept but marked late
func (s *ClassroomService) HandIn(ctx context.Context, token, slug, submissionID string, answers map[string]domain.QuizAnswer) (domain.QuizSubmission, error) {
	sc, sub, a, err := s.ownAttempt(ctx, token, slug, submissionID)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	if sub.Status != domain.SubmissionInProgress {
		return domain.QuizSubmission{}, domain.ErrAttemptFinished
	}
	if answers != nil {
		sub.Answers = cleanAnswers(a, answers)
		if err := s.repo.SaveAnswers(ctx, submissionID, sub.Answers); err != nil {
			return domain.QuizSubmission{}, err
		}
	}
	ext, err := s.repo.Extension(ctx, a.ItemID, sub.StudentID)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	now := s.now()
	points, total := autoGrade(a.Questions, sub.Answers)
	if err := s.repo.HandIn(ctx, submissionID, points, total, !a.Open(now, ext), now); err != nil {
		return domain.QuizSubmission{}, err
	}
	s.changed(ctx, sc, "room", a.RoomID)
	return s.repo.QuizSubmission(ctx, sc.institution.ID, submissionID)
}

// GradeInput is the teacher's grading of one attempt
type GradeInput struct {
	SubmissionID string
	Levels       map[string]string
	OpenPoints   map[string]int
	Feedback     string
}

// GradeSubmission sets the level of each competency the assessment assesses, the points of open questions and the
// feedback. A C needs feedback: it becomes the descriptive conclusion of the record (RVM 094-2020-MINEDU)
func (s *ClassroomService) GradeSubmission(ctx context.Context, token, slug string, in GradeInput) (domain.QuizSubmission, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	sub, err := s.repo.QuizSubmission(ctx, sc.institution.ID, in.SubmissionID)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	_, a, canEdit, _, err := s.assessmentAccess(ctx, token, slug, sub.ItemID)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	if !canEdit {
		return domain.QuizSubmission{}, domain.ErrClassroomReadOnly
	}
	if sub.Status == domain.SubmissionInProgress {
		return domain.QuizSubmission{}, domain.Validation(map[string]string{"submission": "El estudiante aún no entrega este intento."})
	}
	fields := map[string]string{}
	levels := map[string]string{}
	for comp, level := range in.Levels {
		if level == "" {
			continue
		}
		switch {
		case !slices.Contains(a.CompetencyIDs, comp):
			fields["levels"] = "Califica solo las competencias que evalúa."
		case !slices.Contains(domain.Levels, level):
			fields["levels"] = "El nivel de logro es AD, A, B o C."
		default:
			levels[comp] = level
		}
	}
	open := map[string]int{}
	for _, q := range a.Questions {
		if q.Kind != domain.QuestionOpen {
			continue
		}
		if p, ok := in.OpenPoints[q.ID]; ok {
			if p < 0 || p > q.Points {
				fields["openPoints"] = "Los puntos de una pregunta abierta van de 0 a su valor."
			}
			open[q.ID] = p
		}
	}
	in.Feedback = strings.TrimSpace(in.Feedback)
	if utf8.RuneCountInString(in.Feedback) > 1000 {
		fields["feedback"] = "La retroalimentación tiene hasta 1000 caracteres."
	}
	for _, l := range levels {
		if l == "C" && in.Feedback == "" {
			fields["feedback"] = "Con C, escribe qué le falta lograr y cómo se le apoyará: será la conclusión descriptiva."
		}
	}
	if len(levels) == 0 && in.Feedback == "" {
		fields["levels"] = "Pon al menos un nivel o una retroalimentación."
	}
	if len(fields) > 0 {
		return domain.QuizSubmission{}, domain.Validation(fields)
	}
	sub.Levels, sub.OpenPoints, sub.Feedback = levels, open, in.Feedback
	out, err := s.repo.Grade(ctx, sc.institution.ID, sub, sc.userID, sc.userName)
	if err != nil {
		return domain.QuizSubmission{}, err
	}
	s.changed(ctx, sc, "room", a.RoomID)
	return out, nil
}

// SetExtension gives one student more time (nil removes it)
func (s *ClassroomService) SetExtension(ctx context.Context, token, slug, itemID, studentID string, closesAt *time.Time, reason string) error {
	sc, a, canEdit, _, err := s.assessmentAccess(ctx, token, slug, itemID)
	if err != nil {
		return err
	}
	if !canEdit {
		return domain.ErrClassroomReadOnly
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 300 {
		return domain.Validation(map[string]string{"reason": "Máximo 300 caracteres."})
	}
	if closesAt != nil && a.OpensAt != nil && !closesAt.After(*a.OpensAt) {
		return domain.Validation(map[string]string{"closesAt": "La prórroga debe terminar después de la apertura."})
	}
	roster, err := s.repo.Roster(ctx, sc.institution.ID, itemID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(roster, func(r domain.QuizRosterRow) bool { return r.StudentID == studentID }) {
		return domain.ErrNotFound
	}
	if err := s.repo.SetExtension(ctx, itemID, studentID, closesAt, reason, sc.userName); err != nil {
		return err
	}
	s.changed(ctx, sc, "room", a.RoomID)
	return nil
}

// RecordResult says how many students went to the official record and which could not, and why
type RecordResult struct {
	Recorded int
	Skipped  []string
}

// RecordLevels takes the levels of the latest graded attempt of each student to the official record of the open
// term, through AssessmentService.SaveLevels (so the record's own rules apply). The feedback goes as the descriptive
// conclusion. Students whose section has no teaching assignment for the area, or whose sheet the person cannot edit,
// are skipped and listed
func (s *ClassroomService) RecordLevels(ctx context.Context, token, slug, itemID string) (RecordResult, error) {
	if s.grades == nil {
		return RecordResult{}, domain.ErrInternal
	}
	sc, a, canEdit, _, err := s.assessmentAccess(ctx, token, slug, itemID)
	if err != nil {
		return RecordResult{}, err
	}
	if !canEdit {
		return RecordResult{}, domain.ErrClassroomReadOnly
	}
	room, err := s.repo.Room(ctx, sc.institution.ID, a.RoomID)
	if err != nil {
		return RecordResult{}, err
	}
	termID, err := s.repo.OpenTerm(ctx, room.PeriodID, s.now())
	if err != nil {
		return RecordResult{}, err
	}
	if termID == "" {
		return RecordResult{}, domain.ErrNoOpenTerm
	}
	targets, err := s.repo.RecordTargets(ctx, sc.institution.ID, itemID)
	if err != nil {
		return RecordResult{}, err
	}
	if len(targets) == 0 {
		return RecordResult{}, domain.ErrNoGradedLevels
	}
	conclusion := func(f string) string {
		if utf8.RuneCountInString(f) > 1000 {
			return string([]rune(f)[:1000])
		}
		return f
	}
	byAssignment := map[string][]domain.RecordTarget{}
	out := RecordResult{}
	for _, t := range targets {
		if t.AssignmentID == "" {
			out.Skipped = append(out.Skipped, t.StudentID)
			continue
		}
		byAssignment[t.AssignmentID] = append(byAssignment[t.AssignmentID], t)
	}
	var recorded []string
	for assignmentID, group := range byAssignment {
		var marks []domain.CompetencyMark
		for _, t := range group {
			for comp, level := range t.Levels {
				marks = append(marks, domain.CompetencyMark{StudentID: t.StudentID, CompetencyID: comp, TermID: termID, Level: level, Conclusion: conclusion(t.Feedback)})
			}
		}
		if _, err := s.grades.SaveLevels(ctx, token, slug, assignmentID, termID, marks); err != nil {
			// A sheet the person cannot touch (closed term, another teacher's section) skips its students
			var de *domain.Error
			if !errors.As(err, &de) {
				return out, err
			}
			for _, t := range group {
				out.Skipped = append(out.Skipped, t.StudentID)
			}
			continue
		}
		for _, t := range group {
			recorded = append(recorded, t.SubmissionID)
		}
	}
	if len(recorded) > 0 {
		if err := s.repo.MarkRecorded(ctx, recorded, s.now()); err != nil {
			return out, err
		}
		s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "classroom", Action: domain.AuditUpdate, Entity: "assessment", EntityID: itemID,
			Summary: "Llevó al registro los niveles de «" + a.Title + "» de " + itoa(len(recorded)) + " estudiantes"})
	}
	out.Recorded = len(recorded)
	s.changed(ctx, sc, "room", a.RoomID)
	return out, nil
}
