package grpc

import (
	"context"
	"time"

	"api/internal/adapter/inbound/grpc/gen/classroomv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// Online assessments of the classroom server (quiz and task items)

func rfc3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// optionalInstant reads an RFC 3339 instant that may be empty (nil)
func optionalInstant(field, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := parseInstant(field, v)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func toQuizQuestion(q domain.QuizQuestion) *classroomv1.Question {
	out := &classroomv1.Question{Id: q.ID, Kind: q.Kind, Prompt: q.Prompt, Options: q.Options, Accepted: q.Accepted, Points: int32(q.Points)}
	for _, c := range q.Correct {
		out.Correct = append(out.Correct, int32(c))
	}
	return out
}

func toAssessment(a domain.Assessment) *classroomv1.Assessment {
	out := &classroomv1.Assessment{ItemId: a.ItemID, RoomId: a.RoomID, UnitId: a.UnitID, Kind: a.Kind, Title: a.Title, Instructions: a.Instructions,
		Published: a.Published, OpensAt: rfc3339(a.OpensAt), ClosesAt: rfc3339(a.ClosesAt), MaxAttempts: int32(a.MaxAttempts),
		CompetencyIds: a.CompetencyIDs, ShowAnswers: a.ShowAnswers}
	for _, q := range a.Questions {
		out.Questions = append(out.Questions, toQuizQuestion(q))
	}
	return out
}

func toQuizSubmission(s domain.QuizSubmission) *classroomv1.Submission {
	out := &classroomv1.Submission{Id: s.ID, ItemId: s.ItemID, StudentId: s.StudentID, StudentName: s.StudentName, StudentCode: s.StudentCode,
		Attempt: int32(s.Attempt), Status: s.Status, AutoPoints: int32(s.AutoPoints), AutoTotal: int32(s.AutoTotal), Levels: s.Levels,
		Feedback: s.Feedback, StartedAt: rfc3339(&s.StartedAt), SubmittedAt: rfc3339(s.SubmittedAt), Late: s.Late, GradedAt: rfc3339(s.GradedAt),
		GradedName: s.GradedName, RecordedAt: rfc3339(s.RecordedAt), Files: int32(s.Files), SuggestedLevel: service.SuggestedLevel(s.AutoPoints, s.AutoTotal),
		Answers: map[string]*classroomv1.Answer{}, OpenPoints: map[string]int32{}}
	for k, a := range s.Answers {
		ans := &classroomv1.Answer{Text: a.Text}
		for _, c := range a.Choices {
			ans.Choices = append(ans.Choices, int32(c))
		}
		out.Answers[k] = ans
	}
	for k, p := range s.OpenPoints {
		out.OpenPoints[k] = int32(p)
	}
	return out
}

func fromAnswers(in map[string]*classroomv1.Answer) map[string]domain.QuizAnswer {
	if in == nil {
		return nil
	}
	out := make(map[string]domain.QuizAnswer, len(in))
	for k, a := range in {
		ans := domain.QuizAnswer{Text: a.GetText()}
		for _, c := range a.GetChoices() {
			ans.Choices = append(ans.Choices, int(c))
		}
		out[k] = ans
	}
	return out
}

func (s *classroomServer) GetAssessment(ctx context.Context, req *classroomv1.GetAssessmentRequest) (*classroomv1.GetAssessmentResponse, error) {
	v, err := s.classroom.Assessment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetItemId())
	if err != nil {
		return nil, s.fail("GetAssessment", err)
	}
	out := &classroomv1.GetAssessmentResponse{Assessment: toAssessment(v.Assessment), CanEdit: v.CanEdit, Family: v.Family, QuestionsLocked: v.QuestionsLock,
		Extension: rfc3339(v.Extension), OpenNow: v.OpenNow, CanStart: v.CanStart}
	for _, c := range v.Competencies {
		out.Competencies = append(out.Competencies, &classroomv1.Competency{Id: c.ID, Name: c.Name})
	}
	for _, r := range v.Roster {
		row := &classroomv1.RosterRow{StudentId: r.StudentID, StudentName: r.StudentName, StudentCode: r.StudentCode, Attempts: int32(r.Attempts), Extension: rfc3339(r.Extension)}
		if r.Latest != nil {
			row.Latest = toQuizSubmission(*r.Latest)
		}
		out.Roster = append(out.Roster, row)
	}
	for _, a := range v.Attempts {
		out.Attempts = append(out.Attempts, toQuizSubmission(a))
	}
	return out, nil
}

func (s *classroomServer) SaveAssessment(ctx context.Context, req *classroomv1.SaveAssessmentRequest) (*classroomv1.SaveAssessmentResponse, error) {
	opens, err := optionalInstant("opensAt", req.GetOpensAt())
	if err != nil {
		return nil, s.fail("SaveAssessment", err)
	}
	closes, err := optionalInstant("closesAt", req.GetClosesAt())
	if err != nil {
		return nil, s.fail("SaveAssessment", err)
	}
	var questions []domain.QuizQuestion
	for _, q := range req.GetQuestions() {
		dq := domain.QuizQuestion{Kind: q.GetKind(), Prompt: q.GetPrompt(), Options: q.GetOptions(), Accepted: q.GetAccepted(), Points: int(q.GetPoints())}
		for _, c := range q.GetCorrect() {
			dq.Correct = append(dq.Correct, int(c))
		}
		questions = append(questions, dq)
	}
	a, err := s.classroom.SaveAssessment(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Assessment{ItemID: req.GetItemId(), OpensAt: opens, ClosesAt: closes,
		MaxAttempts: int(req.GetMaxAttempts()), CompetencyIDs: req.GetCompetencyIds(), ShowAnswers: req.GetShowAnswers()}, questions, req.GetReplaceQuestions())
	if err != nil {
		return nil, s.fail("SaveAssessment", err)
	}
	return &classroomv1.SaveAssessmentResponse{Assessment: toAssessment(a)}, nil
}

func (s *classroomServer) StartAttempt(ctx context.Context, req *classroomv1.StartAttemptRequest) (*classroomv1.StartAttemptResponse, error) {
	sub, err := s.classroom.StartAttempt(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetItemId())
	if err != nil {
		return nil, s.fail("StartAttempt", err)
	}
	return &classroomv1.StartAttemptResponse{Submission: toQuizSubmission(sub)}, nil
}

func (s *classroomServer) SaveAnswers(ctx context.Context, req *classroomv1.SaveAnswersRequest) (*classroomv1.SaveAnswersResponse, error) {
	if err := s.classroom.SaveAnswers(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSubmissionId(), fromAnswers(req.GetAnswers())); err != nil {
		return nil, s.fail("SaveAnswers", err)
	}
	return &classroomv1.SaveAnswersResponse{}, nil
}

func (s *classroomServer) HandIn(ctx context.Context, req *classroomv1.HandInRequest) (*classroomv1.HandInResponse, error) {
	sub, err := s.classroom.HandIn(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSubmissionId(), fromAnswers(req.GetAnswers()))
	if err != nil {
		return nil, s.fail("HandIn", err)
	}
	return &classroomv1.HandInResponse{Submission: toQuizSubmission(sub)}, nil
}

func (s *classroomServer) GradeSubmission(ctx context.Context, req *classroomv1.GradeSubmissionRequest) (*classroomv1.GradeSubmissionResponse, error) {
	open := map[string]int{}
	for k, v := range req.GetOpenPoints() {
		open[k] = int(v)
	}
	sub, err := s.classroom.GradeSubmission(ctx, bearer(ctx), req.GetInstitutionSlug(), service.GradeInput{SubmissionID: req.GetSubmissionId(), Levels: req.GetLevels(),
		OpenPoints: open, Feedback: req.GetFeedback()})
	if err != nil {
		return nil, s.fail("GradeSubmission", err)
	}
	return &classroomv1.GradeSubmissionResponse{Submission: toQuizSubmission(sub)}, nil
}

func (s *classroomServer) SetExtension(ctx context.Context, req *classroomv1.SetExtensionRequest) (*classroomv1.SetExtensionResponse, error) {
	closes, err := optionalInstant("closesAt", req.GetClosesAt())
	if err != nil {
		return nil, s.fail("SetExtension", err)
	}
	if err := s.classroom.SetExtension(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetItemId(), req.GetStudentId(), closes, req.GetReason()); err != nil {
		return nil, s.fail("SetExtension", err)
	}
	return &classroomv1.SetExtensionResponse{}, nil
}

func (s *classroomServer) RecordLevels(ctx context.Context, req *classroomv1.RecordLevelsRequest) (*classroomv1.RecordLevelsResponse, error) {
	r, err := s.classroom.RecordLevels(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetItemId())
	if err != nil {
		return nil, s.fail("RecordLevels", err)
	}
	return &classroomv1.RecordLevelsResponse{Recorded: int32(r.Recorded), SkippedStudentIds: r.Skipped}, nil
}
