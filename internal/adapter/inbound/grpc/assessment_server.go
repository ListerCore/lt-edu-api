package grpc

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/assessmentv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// assessmentServer exposes service.AssessmentService to joseph-app's server
type assessmentServer struct {
	assessmentv1.UnimplementedAssessmentServiceServer
	assessment *service.AssessmentService
	logger     zerolog.Logger
}

func (s *assessmentServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("assessment grpc call failed")
	}
	return fromDomain(err)
}

func toSubmission(x *domain.Submission) *assessmentv1.Submission {
	if x == nil {
		return nil
	}
	return &assessmentv1.Submission{SubmittedName: x.SubmittedName, SubmittedAt: instant(&x.SubmittedAt)}
}

func toStudentRefs(items []domain.StudentRef) []*assessmentv1.StudentRef {
	out := make([]*assessmentv1.StudentRef, len(items))
	for i, s := range items {
		out[i] = &assessmentv1.StudentRef{Id: s.ID, Code: s.Code, Name: s.Name, Document: s.Document}
	}
	return out
}

func toLevelMarks(items []domain.CompetencyMark) []*assessmentv1.LevelMark {
	out := make([]*assessmentv1.LevelMark, len(items))
	for i, m := range items {
		out[i] = &assessmentv1.LevelMark{StudentId: m.StudentID, CompetencyId: m.CompetencyID, Level: m.Level, Conclusion: m.Conclusion}
	}
	return out
}

func fromLevelMarks(items []*assessmentv1.LevelMark) []domain.CompetencyMark {
	out := make([]domain.CompetencyMark, len(items))
	for i, m := range items {
		out[i] = domain.CompetencyMark{StudentID: m.GetStudentId(), CompetencyID: m.GetCompetencyId(), Level: m.GetLevel(), Conclusion: m.GetConclusion()}
	}
	return out
}

func toGradeSheet(sh service.Sheet) *assessmentv1.Sheet {
	out := &assessmentv1.Sheet{Kind: sh.Kind, SectionId: sh.Section.ID, SectionLabel: sh.Section.Label, LevelName: sh.Section.LevelName, AssignmentId: sh.Assignment.ID,
		SubjectName: sh.Assignment.SubjectName, StaffName: sh.Assignment.StaffName, TermId: sh.TermID, TermName: sh.Term.Name, Locked: sh.Locked, CanEdit: sh.CanEdit,
		PassingGrade: int32Ptr(sh.Passing), Conduct: sh.Conduct, Students: toStudentRefs(sh.Students), Marks: toLevelMarks(sh.Marks), Submitted: toSubmission(sh.Submitted),
		TutorName: sh.Section.TutorName}
	for _, c := range sh.Competencies {
		out.Competencies = append(out.Competencies, &assessmentv1.Competency{Id: c.ID, Code: c.Code, Name: c.Name, Transversal: c.Transversal})
	}
	for _, e := range sh.Evaluations {
		out.Evaluations = append(out.Evaluations, &assessmentv1.Evaluation{Id: e.ID, Name: e.Name, Weight: int32(e.Weight), HeldOn: datePtr(e.HeldOn)})
	}
	for _, x := range sh.Scores {
		out.Scores = append(out.Scores, &assessmentv1.ScoreMark{EvaluationId: x.EvaluationID, StudentId: x.StudentID, Score: int32(x.Score)})
	}
	for _, c := range sh.ConductMarks {
		out.ConductMarks = append(out.ConductMarks, &assessmentv1.ConductMark{StudentId: c.StudentID, Score: int32(c.Score), Note: c.Note})
	}
	for _, c := range sh.Comments {
		out.Comments = append(out.Comments, &assessmentv1.TutorComment{StudentId: c.StudentID, Comment: c.Comment})
	}
	return out
}

func toCardCompetencies(items []service.CardCompetency) []*assessmentv1.CardCompetency {
	out := make([]*assessmentv1.CardCompetency, len(items))
	for i, c := range items {
		out[i] = &assessmentv1.CardCompetency{Id: c.ID, Name: c.Name, Levels: c.Levels, Conclusion: c.Conclusion, Conclusions: c.Conclusions}
	}
	return out
}

func toReportCard(c service.ReportCard) *assessmentv1.ReportCard {
	out := &assessmentv1.ReportCard{Institution: c.Institution, Kind: c.Kind, Student: &assessmentv1.StudentRef{Id: c.Student.ID, Code: c.Student.Code, Name: c.Student.Name, Document: c.Student.Document},
		SectionLabel: c.Section.Label, LevelName: c.Section.LevelName, TutorName: c.Section.TutorName, PeriodId: c.PeriodID, PeriodName: c.PeriodName, Scale: c.Scale,
		PassingGrade: int32Ptr(c.Passing), TermId: c.TermID, Transversal: toCardCompetencies(c.Transversal), TutorComment: c.TutorComment, Provisional: c.Provisional,
		Dre: c.Dre, Ugel: c.Ugel, ModularCode: c.ModularCode, Logo: c.Logo, GradeName: c.Section.GradeName, SectionName: c.Section.SectionName, Final: c.Final, IssuedOn: formatDate(c.IssuedOn), TermCount: int32(c.TermCount)}
	for _, t := range c.Terms {
		out.Terms = append(out.Terms, &assessmentv1.CardTerm{Id: t.ID, Name: t.Name, Status: t.Status, Conduct: int32Ptr(t.Conduct), Comment: t.Comment,
			Attendance: &assessmentv1.AttendanceTally{Present: int32(t.Attendance.Present), Late: int32(t.Attendance.Late), Absent: int32(t.Attendance.Absent), Justified: int32(t.Attendance.Justified)}})
	}
	for _, sub := range c.Subjects {
		row := &assessmentv1.CardSubject{Id: sub.ID, Name: sub.Name, Kind: sub.Kind, TeacherName: sub.TeacherName, Competencies: toCardCompetencies(sub.Competencies), Averages: map[string]float64{}}
		for k, v := range sub.Averages {
			if v != nil {
				row.Averages[k] = *v
			}
		}
		for _, e := range sub.Evaluations {
			row.Evaluations = append(row.Evaluations, &assessmentv1.CardEvaluation{Name: e.Name, Weight: int32(e.Weight), Score: int32Ptr(e.Score)})
		}
		out.Subjects = append(out.Subjects, row)
	}
	for _, p := range c.Periods {
		out.Periods = append(out.Periods, &assessmentv1.PeriodRef{Id: p.ID, Name: p.Name})
	}
	return out
}

func (s *assessmentServer) GetGradebook(ctx context.Context, req *assessmentv1.GetGradebookRequest) (*assessmentv1.GetGradebookResponse, error) {
	g, err := s.assessment.Gradebook(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), req.GetTermId())
	if err != nil {
		return nil, s.fail("GetGradebook", err)
	}
	out := &assessmentv1.GetGradebookResponse{Periods: toPeriods(g.Periods), TermId: g.TermID, Locked: g.Locked, Scale: g.Scale, PassingGrade: int32Ptr(g.Passing),
		Conduct: g.Conduct, CanManage: g.CanManage, OwnOnly: g.OwnOnly}
	if g.Period.ID != "" {
		out.Period = toPeriod(g.Period)
	}
	for _, p := range g.Sheets {
		out.Sheets = append(out.Sheets, &assessmentv1.SheetProgress{SectionId: p.SectionID, SectionLabel: p.SectionLabel, AssignmentId: p.AssignmentID, SubjectId: p.SubjectID,
			SubjectName: p.SubjectName, StaffId: p.StaffID, StaffName: p.StaffName, Students: int32(p.Students), Expected: int32(p.Expected), Filled: int32(p.Filled),
			Evaluations: int32(p.Evaluations), Submitted: toSubmission(p.Submitted)})
	}
	for _, t := range g.Tutor {
		out.Tutor = append(out.Tutor, &assessmentv1.TutorProgress{SectionId: t.SectionID, SectionLabel: t.SectionLabel, TutorName: t.TutorName, Students: int32(t.Students),
			Expected: int32(t.Expected), Filled: int32(t.Filled), Submitted: toSubmission(t.Submitted)})
	}
	return out, nil
}

func (s *assessmentServer) GetSheet(ctx context.Context, req *assessmentv1.GetSheetRequest) (*assessmentv1.GetSheetResponse, error) {
	sh, err := s.assessment.Sheet(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId(), req.GetTermId())
	if err != nil {
		return nil, s.fail("GetSheet", err)
	}
	return &assessmentv1.GetSheetResponse{Sheet: toGradeSheet(sh)}, nil
}

func (s *assessmentServer) SaveLevels(ctx context.Context, req *assessmentv1.SaveLevelsRequest) (*assessmentv1.SaveLevelsResponse, error) {
	sh, err := s.assessment.SaveLevels(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId(), req.GetTermId(), fromLevelMarks(req.GetMarks()))
	if err != nil {
		return nil, s.fail("SaveLevels", err)
	}
	return &assessmentv1.SaveLevelsResponse{Sheet: toGradeSheet(sh)}, nil
}

func (s *assessmentServer) SaveEvaluation(ctx context.Context, req *assessmentv1.SaveEvaluationRequest) (*assessmentv1.SaveEvaluationResponse, error) {
	held, err := optionalDate("heldOn", req.GetHeldOn())
	if err != nil {
		return nil, err
	}
	sh, err := s.assessment.SaveEvaluation(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId(), req.GetTermId(),
		service.EvaluationInput{ID: req.GetId(), Name: req.GetName(), Weight: int(req.GetWeight()), HeldOn: held})
	if err != nil {
		return nil, s.fail("SaveEvaluation", err)
	}
	return &assessmentv1.SaveEvaluationResponse{Sheet: toGradeSheet(sh)}, nil
}

func (s *assessmentServer) DeleteEvaluation(ctx context.Context, req *assessmentv1.DeleteEvaluationRequest) (*assessmentv1.DeleteEvaluationResponse, error) {
	sh, err := s.assessment.DeleteEvaluation(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId(), req.GetTermId(), req.GetId())
	if err != nil {
		return nil, s.fail("DeleteEvaluation", err)
	}
	return &assessmentv1.DeleteEvaluationResponse{Sheet: toGradeSheet(sh)}, nil
}

func (s *assessmentServer) SaveScores(ctx context.Context, req *assessmentv1.SaveScoresRequest) (*assessmentv1.SaveScoresResponse, error) {
	scores := map[string]*int{}
	for _, x := range req.GetScores() {
		scores[x.GetStudentId()] = intPtr(x.Score)
	}
	sh, err := s.assessment.SaveScores(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId(), req.GetTermId(), req.GetEvaluationId(), scores)
	if err != nil {
		return nil, s.fail("SaveScores", err)
	}
	return &assessmentv1.SaveScoresResponse{Sheet: toGradeSheet(sh)}, nil
}

func (s *assessmentServer) GetTutorSheet(ctx context.Context, req *assessmentv1.GetTutorSheetRequest) (*assessmentv1.GetTutorSheetResponse, error) {
	sh, err := s.assessment.TutorSheet(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetTermId())
	if err != nil {
		return nil, s.fail("GetTutorSheet", err)
	}
	return &assessmentv1.GetTutorSheetResponse{Sheet: toGradeSheet(sh)}, nil
}

func (s *assessmentServer) SaveTutorSheet(ctx context.Context, req *assessmentv1.SaveTutorSheetRequest) (*assessmentv1.SaveTutorSheetResponse, error) {
	in := service.TutorInput{Marks: fromLevelMarks(req.GetMarks()), Conduct: map[string]*int{}, Notes: map[string]string{}, Comments: map[string]string{}}
	for _, c := range req.GetConduct() {
		in.Conduct[c.GetStudentId()] = intPtr(c.Score)
		in.Notes[c.GetStudentId()] = c.GetNote()
	}
	for _, c := range req.GetComments() {
		in.Comments[c.GetStudentId()] = c.GetComment()
	}
	sh, err := s.assessment.SaveTutorSheet(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetTermId(), in)
	if err != nil {
		return nil, s.fail("SaveTutorSheet", err)
	}
	return &assessmentv1.SaveTutorSheetResponse{Sheet: toGradeSheet(sh)}, nil
}

func (s *assessmentServer) Submit(ctx context.Context, req *assessmentv1.SubmitRequest) (*assessmentv1.SubmitResponse, error) {
	if err := s.assessment.Submit(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetAssignmentId(), req.GetTermId(), req.GetSent()); err != nil {
		return nil, s.fail("Submit", err)
	}
	return &assessmentv1.SubmitResponse{}, nil
}

func (s *assessmentServer) GetReportCard(ctx context.Context, req *assessmentv1.GetReportCardRequest) (*assessmentv1.GetReportCardResponse, error) {
	c, err := s.assessment.ReportCard(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetPeriodId(), req.GetTermId())
	if err != nil {
		return nil, s.fail("GetReportCard", err)
	}
	return &assessmentv1.GetReportCardResponse{Card: toReportCard(c)}, nil
}

func (s *assessmentServer) GetSectionCards(ctx context.Context, req *assessmentv1.GetSectionCardsRequest) (*assessmentv1.GetSectionCardsResponse, error) {
	cards, err := s.assessment.SectionCards(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetTermId())
	if err != nil {
		return nil, s.fail("GetSectionCards", err)
	}
	out := &assessmentv1.GetSectionCardsResponse{}
	for i, c := range cards {
		// The logo is the same for the whole section: only the first card carries it
		if i > 0 {
			c.Logo = ""
		}
		out.Cards = append(out.Cards, toReportCard(c))
	}
	return out, nil
}

func (s *assessmentServer) MyReportCards(ctx context.Context, req *assessmentv1.MyReportCardsRequest) (*assessmentv1.MyReportCardsResponse, error) {
	cards, err := s.assessment.MyCards(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("MyReportCards", err)
	}
	out := &assessmentv1.MyReportCardsResponse{}
	for _, f := range cards {
		x := &assessmentv1.FamilyCard{StudentId: f.Student.ID, StudentName: f.Student.Name, Message: f.Message}
		if f.Card != nil {
			x.Card = toReportCard(*f.Card)
		}
		out.Cards = append(out.Cards, x)
	}
	return out, nil
}
