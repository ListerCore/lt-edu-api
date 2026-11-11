package grpc

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/examsv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// examsServer exposes service.ExamsService to joseph-app's server
type examsServer struct {
	examsv1.UnimplementedExamsServiceServer
	exams  *service.ExamsService
	logger zerolog.Logger
}

func (s *examsServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("exams grpc call failed")
	}
	return fromDomain(err)
}

func toQuestion(q domain.Question, userID string) *examsv1.Question {
	return &examsv1.Question{Id: q.ID, CourseCode: q.CourseCode, Topic: q.Topic, Stem: q.Stem, Options: q.Options, Answer: q.Answer, Difficulty: q.Difficulty,
		Explanation: q.Explanation, AuthorName: q.AuthorName, Mine: userID != "" && q.CreatedBy == userID, Uses: int32(q.Uses)}
}

func toMock(m domain.MockExam) *examsv1.MockExam {
	out := &examsv1.MockExam{Id: m.ID, PeriodId: m.PeriodID, PeriodName: m.PeriodName, Name: m.Name, HeldOn: formatDate(m.HeldOn), DurationMinutes: int32(m.Duration),
		PointsRight: m.PointsRight, PointsWrong: m.PointsWrong, Status: m.Status, Notes: m.Notes, Questions: int32(m.Questions), Keyed: int32(m.Keyed), Sheets: int32(m.Sheets)}
	for _, c := range m.Courses {
		out.Courses = append(out.Courses, &examsv1.MockCourse{Code: c.Code, Name: c.Name, Questions: int32(c.Questions)})
	}
	for _, w := range m.Weights {
		out.Weights = append(out.Weights, &examsv1.MockWeight{LevelId: w.LevelID, CourseCode: w.CourseCode, Weight: w.Weight})
	}
	return out
}

func toMockDetail(d service.MockDetail) *examsv1.GetMockResponse {
	out := &examsv1.GetMockResponse{Mock: toMock(d.Mock), CanEdit: d.CanEdit, Students: int32(d.Students)}
	for _, k := range d.Key {
		out.Key = append(out.Key, &examsv1.KeyItem{Number: int32(k.Number), CourseCode: k.CourseCode, QuestionId: k.QuestionID, Answer: k.Answer})
	}
	return out
}

func fromCourses(in []*examsv1.MockCourse) []domain.MockCourse {
	out := make([]domain.MockCourse, len(in))
	for i, c := range in {
		out[i] = domain.MockCourse{Code: c.GetCode(), Questions: int(c.GetQuestions()), Position: i}
	}
	return out
}

func fromWeights(in []*examsv1.MockWeight) []domain.MockWeight {
	out := make([]domain.MockWeight, len(in))
	for i, w := range in {
		out[i] = domain.MockWeight{LevelID: w.GetLevelId(), CourseCode: w.GetCourseCode(), Weight: w.GetWeight()}
	}
	return out
}

func toSheetMsg(a domain.AnswerSheet) *examsv1.AnswerSheet {
	out := &examsv1.AnswerSheet{StudentId: a.StudentID, StudentCode: a.StudentCode, StudentName: a.StudentName, SectionId: a.SectionID, SectionLabel: a.SectionLabel,
		LevelId: a.LevelID, LevelName: a.LevelName, Answers: a.Answers, Source: a.Source, Score: a.Score, Right: int32(a.Right), Wrong: int32(a.Wrong), Blank: int32(a.Blank),
		ByCourse: map[string]*examsv1.CourseScore{}, RankSection: int32(a.RankSection), RankLevel: int32(a.RankLevel), RankAll: int32(a.RankAll)}
	for k, c := range a.ByCourse {
		out.ByCourse[k] = &examsv1.CourseScore{Right: int32(c.Right), Wrong: int32(c.Wrong), Blank: int32(c.Blank), Score: c.Score}
	}
	return out
}

func toAdmission(a domain.Admission) *examsv1.Admission {
	return &examsv1.Admission{Id: a.ID, StudentId: a.StudentID, StudentName: a.StudentName, StudentCode: a.StudentCode, University: a.University, Career: a.Career,
		Exam: a.Exam, AdmittedOn: formatDate(a.AdmittedOn), Place: int32Ptr(a.Place), Score: a.Score}
}

func toStudentExams(x service.StudentExams) *examsv1.StudentExams {
	out := &examsv1.StudentExams{StudentId: x.Student.ID, StudentName: x.Student.Name}
	for _, m := range x.Mocks {
		out.Mocks = append(out.Mocks, &examsv1.StudentMock{Mock: toMock(m.Mock), Sheet: toSheetMsg(m.Sheet), Takers: int32(m.Takers), InLevel: int32(m.InLevel)})
	}
	for _, a := range x.Admissions {
		out.Admissions = append(out.Admissions, toAdmission(a))
	}
	return out
}

func toReport(r service.LoadReport) *examsv1.LoadReport {
	return &examsv1.LoadReport{Saved: int32(r.Saved), Unknown: r.Unknown, OtherArea: r.OtherArea}
}

func (s *examsServer) GetOverview(ctx context.Context, req *examsv1.GetOverviewRequest) (*examsv1.GetOverviewResponse, error) {
	o, err := s.exams.Overview(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("GetOverview", err)
	}
	out := &examsv1.GetOverviewResponse{Periods: toPeriods(o.Periods), CanManage: o.CanManage, CanWriteBank: o.CanWriteBank, CanLoad: o.CanLoad}
	if o.Period.ID != "" {
		out.Period = toPeriod(o.Period)
	}
	for _, l := range o.Levels {
		out.Levels = append(out.Levels, &examsv1.Level{Id: l.ID, Code: l.Code, Name: l.Name})
	}
	for _, c := range o.Courses {
		hours := map[string]int32{}
		for k, v := range c.Hours {
			hours[k] = int32(v)
		}
		out.Courses = append(out.Courses, &examsv1.Course{Code: c.Code, Name: c.Name, Topics: c.Topics, Hours: hours, Bank: int32(c.Bank)})
	}
	for _, m := range o.Mocks {
		out.Mocks = append(out.Mocks, toMock(m))
	}
	return out, nil
}

func (s *examsServer) ListQuestions(ctx context.Context, req *examsv1.ListQuestionsRequest) (*examsv1.ListQuestionsResponse, error) {
	p, uid, err := s.questions(ctx, req)
	if err != nil {
		return nil, s.fail("ListQuestions", err)
	}
	out := &examsv1.ListQuestionsResponse{Total: int32(p.Total)}
	for _, q := range p.Items {
		out.Questions = append(out.Questions, toQuestion(q, uid))
	}
	return out, nil
}

func (s *examsServer) questions(ctx context.Context, req *examsv1.ListQuestionsRequest) (service.QuestionPage, string, error) {
	p, err := s.exams.Questions(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.QuestionFilter{CourseCode: req.GetCourseCode(), Topic: req.GetTopic(),
		Difficulty: req.GetDifficulty(), Query: req.GetQuery(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset())})
	if err != nil {
		return p, "", err
	}
	return p, s.exams.UserID(ctx, bearer(ctx)), nil
}

func (s *examsServer) SaveQuestion(ctx context.Context, req *examsv1.SaveQuestionRequest) (*examsv1.SaveQuestionResponse, error) {
	q := req.GetQuestion()
	out, err := s.exams.SaveQuestion(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Question{ID: q.GetId(), CourseCode: q.GetCourseCode(), Topic: q.GetTopic(), Stem: q.GetStem(),
		Options: q.GetOptions(), Answer: q.GetAnswer(), Difficulty: q.GetDifficulty(), Explanation: q.GetExplanation()})
	if err != nil {
		return nil, s.fail("SaveQuestion", err)
	}
	return &examsv1.SaveQuestionResponse{Question: toQuestion(out, out.CreatedBy)}, nil
}

func (s *examsServer) DeleteQuestion(ctx context.Context, req *examsv1.DeleteQuestionRequest) (*examsv1.DeleteQuestionResponse, error) {
	if err := s.exams.DeleteQuestion(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteQuestion", err)
	}
	return &examsv1.DeleteQuestionResponse{}, nil
}

func (s *examsServer) GetMock(ctx context.Context, req *examsv1.GetMockRequest) (*examsv1.GetMockResponse, error) {
	d, err := s.exams.Mock(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetMock", err)
	}
	return toMockDetail(d), nil
}

func (s *examsServer) SaveMock(ctx context.Context, req *examsv1.SaveMockRequest) (*examsv1.SaveMockResponse, error) {
	m := req.GetMock()
	held, err := optionalDate("heldOn", m.GetHeldOn())
	if err != nil {
		return nil, err
	}
	in := domain.MockExam{ID: m.GetId(), PeriodID: m.GetPeriodId(), Name: m.GetName(), Duration: int(m.GetDurationMinutes()), PointsRight: m.GetPointsRight(), PointsWrong: m.GetPointsWrong(), Notes: m.GetNotes()}
	if held != nil {
		in.HeldOn = *held
	}
	out, err := s.exams.SaveMock(ctx, bearer(ctx), req.GetInstitutionSlug(), in)
	if err != nil {
		return nil, s.fail("SaveMock", err)
	}
	return &examsv1.SaveMockResponse{Mock: toMock(out)}, nil
}

func (s *examsServer) DeleteMock(ctx context.Context, req *examsv1.DeleteMockRequest) (*examsv1.DeleteMockResponse, error) {
	if err := s.exams.DeleteMock(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteMock", err)
	}
	return &examsv1.DeleteMockResponse{}, nil
}

func (s *examsServer) SuggestStructure(ctx context.Context, req *examsv1.SuggestStructureRequest) (*examsv1.SuggestStructureResponse, error) {
	c, w, err := s.exams.StructureTemplate(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetLevelIds(), int(req.GetTotalQuestions()))
	if err != nil {
		return nil, s.fail("SuggestStructure", err)
	}
	m := toMock(domain.MockExam{Courses: c, Weights: w})
	return &examsv1.SuggestStructureResponse{Courses: m.Courses, Weights: m.Weights}, nil
}

func (s *examsServer) SetStructure(ctx context.Context, req *examsv1.SetStructureRequest) (*examsv1.SetStructureResponse, error) {
	d, err := s.exams.SetStructure(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), fromCourses(req.GetCourses()), fromWeights(req.GetWeights()))
	if err != nil {
		return nil, s.fail("SetStructure", err)
	}
	return &examsv1.SetStructureResponse{Mock: toMockDetail(d)}, nil
}

func (s *examsServer) FillFromBank(ctx context.Context, req *examsv1.FillFromBankRequest) (*examsv1.FillFromBankResponse, error) {
	d, n, err := s.exams.FillFromBank(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("FillFromBank", err)
	}
	return &examsv1.FillFromBankResponse{Mock: toMockDetail(d), Filled: int32(n)}, nil
}

func (s *examsServer) SaveKey(ctx context.Context, req *examsv1.SaveKeyRequest) (*examsv1.SaveKeyResponse, error) {
	items := make([]domain.KeyItem, len(req.GetItems()))
	for i, k := range req.GetItems() {
		items[i] = domain.KeyItem{Number: int(k.GetNumber()), QuestionID: k.GetQuestionId(), Answer: k.GetAnswer()}
	}
	d, err := s.exams.SaveKey(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), items)
	if err != nil {
		return nil, s.fail("SaveKey", err)
	}
	return &examsv1.SaveKeyResponse{Mock: toMockDetail(d)}, nil
}

func (s *examsServer) PublishMock(ctx context.Context, req *examsv1.PublishMockRequest) (*examsv1.PublishMockResponse, error) {
	d, err := s.exams.Publish(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetPublished())
	if err != nil {
		return nil, s.fail("PublishMock", err)
	}
	return &examsv1.PublishMockResponse{Mock: toMockDetail(d)}, nil
}

func (s *examsServer) GetBooklet(ctx context.Context, req *examsv1.GetBookletRequest) (*examsv1.GetBookletResponse, error) {
	m, items, err := s.exams.Booklet(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetBooklet", err)
	}
	out := &examsv1.GetBookletResponse{Mock: toMock(m)}
	for _, it := range items {
		x := &examsv1.BookletItem{Number: int32(it.Number), Course: it.Course, Answer: it.Answer}
		if it.Question != nil {
			x.Question = toQuestion(*it.Question, "")
		}
		out.Items = append(out.Items, x)
	}
	return out, nil
}

func (s *examsServer) LoadSheets(ctx context.Context, req *examsv1.LoadSheetsRequest) (*examsv1.LoadSheetsResponse, error) {
	entries := make([]service.SheetEntry, len(req.GetEntries()))
	for i, e := range req.GetEntries() {
		entries[i] = service.SheetEntry{StudentID: e.GetStudentId(), Code: e.GetCode(), Answers: e.GetAnswers()}
	}
	r, err := s.exams.LoadSheets(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), "manual", entries)
	if err != nil {
		return nil, s.fail("LoadSheets", err)
	}
	return &examsv1.LoadSheetsResponse{Report: toReport(r)}, nil
}

func (s *examsServer) LoadReaderFile(ctx context.Context, req *examsv1.LoadReaderFileRequest) (*examsv1.LoadReaderFileResponse, error) {
	r, err := s.exams.LoadFile(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetText())
	if err != nil {
		return nil, s.fail("LoadReaderFile", err)
	}
	return &examsv1.LoadReaderFileResponse{Report: toReport(r)}, nil
}

func (s *examsServer) DeleteSheet(ctx context.Context, req *examsv1.DeleteSheetRequest) (*examsv1.DeleteSheetResponse, error) {
	if err := s.exams.DeleteSheet(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStudentId()); err != nil {
		return nil, s.fail("DeleteSheet", err)
	}
	return &examsv1.DeleteSheetResponse{}, nil
}

func (s *examsServer) GetResults(ctx context.Context, req *examsv1.GetResultsRequest) (*examsv1.GetResultsResponse, error) {
	r, err := s.exams.Results(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetResults", err)
	}
	out := &examsv1.GetResultsResponse{Mock: toMock(r.Mock), Average: r.Average, Best: r.Best}
	for _, a := range r.Sheets {
		out.Sheets = append(out.Sheets, toSheetMsg(a))
	}
	for _, c := range r.Courses {
		out.Courses = append(out.Courses, &examsv1.CourseStat{Code: c.Code, Name: c.Name, Average: c.Average, Right: c.Right})
	}
	for _, m := range r.Missing {
		out.Missing = append(out.Missing, &examsv1.ExamStudent{Id: m.ID, Code: m.Code, Name: m.Name, SectionLabel: m.SectionLabel, LevelName: m.LevelName})
	}
	return out, nil
}

func (s *examsServer) GetStudentExams(ctx context.Context, req *examsv1.GetStudentExamsRequest) (*examsv1.GetStudentExamsResponse, error) {
	x, err := s.exams.StudentHistory(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId())
	if err != nil {
		return nil, s.fail("GetStudentExams", err)
	}
	return &examsv1.GetStudentExamsResponse{Exams: toStudentExams(x)}, nil
}

func (s *examsServer) MyExams(ctx context.Context, req *examsv1.MyExamsRequest) (*examsv1.MyExamsResponse, error) {
	items, err := s.exams.Mine(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("MyExams", err)
	}
	out := &examsv1.MyExamsResponse{}
	for _, x := range items {
		out.Students = append(out.Students, toStudentExams(x))
	}
	return out, nil
}

func (s *examsServer) ListAdmissions(ctx context.Context, req *examsv1.ListAdmissionsRequest) (*examsv1.ListAdmissionsResponse, error) {
	list, can, err := s.exams.Admissions(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListAdmissions", err)
	}
	out := &examsv1.ListAdmissionsResponse{CanManage: can}
	for _, a := range list {
		out.Admissions = append(out.Admissions, toAdmission(a))
	}
	return out, nil
}

func (s *examsServer) SaveAdmission(ctx context.Context, req *examsv1.SaveAdmissionRequest) (*examsv1.SaveAdmissionResponse, error) {
	a := req.GetAdmission()
	on, err := optionalDate("admittedOn", a.GetAdmittedOn())
	if err != nil {
		return nil, err
	}
	in := domain.Admission{ID: a.GetId(), StudentID: a.GetStudentId(), University: a.GetUniversity(), Career: a.GetCareer(), Exam: a.GetExam(), Place: intPtr(a.Place), Score: a.Score}
	if on != nil {
		in.AdmittedOn = *on
	}
	out, err := s.exams.SaveAdmission(ctx, bearer(ctx), req.GetInstitutionSlug(), in)
	if err != nil {
		return nil, s.fail("SaveAdmission", err)
	}
	return &examsv1.SaveAdmissionResponse{Admission: toAdmission(out)}, nil
}

func (s *examsServer) DeleteAdmission(ctx context.Context, req *examsv1.DeleteAdmissionRequest) (*examsv1.DeleteAdmissionResponse, error) {
	if err := s.exams.DeleteAdmission(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteAdmission", err)
	}
	return &examsv1.DeleteAdmissionResponse{}, nil
}
