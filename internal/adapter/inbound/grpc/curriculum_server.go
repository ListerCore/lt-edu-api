package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/academicv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// curriculumServer exposes service.CurriculumService to lt-edu-app's server
type curriculumServer struct {
	academicv1.UnimplementedCurriculumServiceServer
	curricula *service.CurriculumService
	logger    zerolog.Logger
}

func (s *curriculumServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("curriculum grpc call failed")
	}
	return fromDomain(err)
}

func toCompetency(c domain.Competency) *academicv1.Competency {
	return &academicv1.Competency{Id: c.ID, SubjectId: c.SubjectID, Kind: c.Kind, Code: c.Code, Name: c.Name, Position: int32(c.Position)}
}

func toCompetencies(items []domain.Competency) []*academicv1.Competency {
	out := make([]*academicv1.Competency, len(items))
	for i, c := range items {
		out[i] = toCompetency(c)
	}
	return out
}

func toSubject(s domain.Subject) *academicv1.Subject {
	grades := s.GradeIDs
	if grades == nil {
		grades = []string{}
	}
	return &academicv1.Subject{
		Id: s.ID, ModuleId: s.ModuleID, Kind: s.Kind, Component: s.Component, Code: s.Code, Name: s.Name, Description: s.Description,
		WeeklyHours: int32Ptr(s.WeeklyHours), TheoryCredits: int32Ptr(s.TheoryCredits), PracticeCredits: int32Ptr(s.PracticeCredits),
		Position: int32(s.Position), GradeIds: grades, Competencies: toCompetencies(s.Competencies),
	}
}

func toModule(m domain.Module) *academicv1.Module {
	return &academicv1.Module{Id: m.ID, Code: m.Code, Name: m.Name, Description: m.Description, Position: int32(m.Position)}
}

func toCurriculum(c domain.Curriculum) *academicv1.Curriculum {
	out := &academicv1.Curriculum{
		Id: c.ID, LevelId: c.LevelID, LevelName: c.LevelName, Name: c.Name, Notes: c.Notes, Status: c.Status,
		UpdatedAt: c.UpdatedAt.Format(time.RFC3339), SubjectCount: int32(c.SubjectCount), TotalCredits: int32(c.TotalCredits),
		Transversal: toCompetencies(c.Transversal),
	}
	if c.ActivatedAt != nil {
		out.ActivatedAt = c.ActivatedAt.Format(time.RFC3339)
	}
	for _, m := range c.Modules {
		out.Modules = append(out.Modules, toModule(m))
	}
	for _, sub := range c.Subjects {
		out.Subjects = append(out.Subjects, toSubject(sub))
	}
	return out
}

func subjectInput(in *academicv1.SubjectInput) domain.SubjectInput {
	return domain.SubjectInput{
		ModuleID: in.GetModuleId(), Kind: in.GetKind(), Component: in.GetComponent(), Code: in.GetCode(), Name: in.GetName(), Description: in.GetDescription(),
		WeeklyHours: intPtr(in.WeeklyHours), TheoryCredits: intPtr(in.TheoryCredits), PracticeCredits: intPtr(in.PracticeCredits), GradeIDs: in.GetGradeIds(),
	}
}

func (s *curriculumServer) detailResponse(d service.CurriculumDetail) (*academicv1.Curriculum, *academicv1.AcademicContext, *academicv1.Level) {
	return toCurriculum(d.Curriculum), toContext(d.Context), toLevel(d.Level)
}

// ---------- plans ----------

func (s *curriculumServer) ListCurricula(ctx context.Context, req *academicv1.ListCurriculaRequest) (*academicv1.ListCurriculaResponse, error) {
	o, err := s.curricula.Overview(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListCurricula", err)
	}
	out := &academicv1.ListCurriculaResponse{Context: toContext(o.Context), Levels: toLevels(o.Levels), TemplateLevelIds: o.TemplateLevels}
	for _, c := range o.Curricula {
		out.Curricula = append(out.Curricula, toCurriculum(c))
	}
	return out, nil
}

func (s *curriculumServer) GetCurriculum(ctx context.Context, req *academicv1.GetCurriculumRequest) (*academicv1.GetCurriculumResponse, error) {
	d, err := s.curricula.Get(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetCurriculum", err)
	}
	c, actx, level := s.detailResponse(d)
	return &academicv1.GetCurriculumResponse{Context: actx, Curriculum: c, Level: level}, nil
}

func (s *curriculumServer) CreateCurriculum(ctx context.Context, req *academicv1.CreateCurriculumRequest) (*academicv1.CreateCurriculumResponse, error) {
	c, err := s.curricula.Create(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetLevelId(), req.GetName(), req.GetFromTemplate())
	if err != nil {
		return nil, s.fail("CreateCurriculum", err)
	}
	return &academicv1.CreateCurriculumResponse{Curriculum: toCurriculum(c)}, nil
}

func (s *curriculumServer) DuplicateCurriculum(ctx context.Context, req *academicv1.DuplicateCurriculumRequest) (*academicv1.DuplicateCurriculumResponse, error) {
	c, err := s.curricula.Duplicate(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetName())
	if err != nil {
		return nil, s.fail("DuplicateCurriculum", err)
	}
	return &academicv1.DuplicateCurriculumResponse{Curriculum: toCurriculum(c)}, nil
}

func (s *curriculumServer) UpdateCurriculum(ctx context.Context, req *academicv1.UpdateCurriculumRequest) (*academicv1.UpdateCurriculumResponse, error) {
	d, err := s.curricula.Update(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), domain.CurriculumInput{Name: req.GetName(), Notes: req.GetNotes()})
	if err != nil {
		return nil, s.fail("UpdateCurriculum", err)
	}
	return &academicv1.UpdateCurriculumResponse{Curriculum: toCurriculum(d.Curriculum)}, nil
}

func (s *curriculumServer) SetCurriculumStatus(ctx context.Context, req *academicv1.SetCurriculumStatusRequest) (*academicv1.SetCurriculumStatusResponse, error) {
	d, err := s.curricula.SetStatus(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus())
	if err != nil {
		return nil, s.fail("SetCurriculumStatus", err)
	}
	return &academicv1.SetCurriculumStatusResponse{Curriculum: toCurriculum(d.Curriculum)}, nil
}

func (s *curriculumServer) DeleteCurriculum(ctx context.Context, req *academicv1.DeleteCurriculumRequest) (*academicv1.DeleteCurriculumResponse, error) {
	if err := s.curricula.Delete(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteCurriculum", err)
	}
	return &academicv1.DeleteCurriculumResponse{}, nil
}

// ---------- modules ----------

func (s *curriculumServer) CreateModule(ctx context.Context, req *academicv1.CreateModuleRequest) (*academicv1.CreateModuleResponse, error) {
	m, err := s.curricula.CreateModule(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetCurriculumId(), domain.ModuleInput{Code: req.GetCode(), Name: req.GetName(), Description: req.GetDescription()})
	if err != nil {
		return nil, s.fail("CreateModule", err)
	}
	return &academicv1.CreateModuleResponse{Module: toModule(m)}, nil
}

func (s *curriculumServer) UpdateModule(ctx context.Context, req *academicv1.UpdateModuleRequest) (*academicv1.UpdateModuleResponse, error) {
	m, err := s.curricula.UpdateModule(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), domain.ModuleInput{Code: req.GetCode(), Name: req.GetName(), Description: req.GetDescription()})
	if err != nil {
		return nil, s.fail("UpdateModule", err)
	}
	return &academicv1.UpdateModuleResponse{Module: toModule(m)}, nil
}

func (s *curriculumServer) DeleteModule(ctx context.Context, req *academicv1.DeleteModuleRequest) (*academicv1.DeleteModuleResponse, error) {
	if err := s.curricula.DeleteModule(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteModule", err)
	}
	return &academicv1.DeleteModuleResponse{}, nil
}

// ---------- subjects ----------

func (s *curriculumServer) CreateSubject(ctx context.Context, req *academicv1.CreateSubjectRequest) (*academicv1.CreateSubjectResponse, error) {
	sub, err := s.curricula.CreateSubject(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetCurriculumId(), subjectInput(req.GetSubject()))
	if err != nil {
		return nil, s.fail("CreateSubject", err)
	}
	return &academicv1.CreateSubjectResponse{Subject: toSubject(sub)}, nil
}

func (s *curriculumServer) UpdateSubject(ctx context.Context, req *academicv1.UpdateSubjectRequest) (*academicv1.UpdateSubjectResponse, error) {
	sub, err := s.curricula.UpdateSubject(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), subjectInput(req.GetSubject()))
	if err != nil {
		return nil, s.fail("UpdateSubject", err)
	}
	return &academicv1.UpdateSubjectResponse{Subject: toSubject(sub)}, nil
}

func (s *curriculumServer) DeleteSubject(ctx context.Context, req *academicv1.DeleteSubjectRequest) (*academicv1.DeleteSubjectResponse, error) {
	if err := s.curricula.DeleteSubject(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteSubject", err)
	}
	return &academicv1.DeleteSubjectResponse{}, nil
}

func (s *curriculumServer) ReorderSubjects(ctx context.Context, req *academicv1.ReorderSubjectsRequest) (*academicv1.ReorderSubjectsResponse, error) {
	if err := s.curricula.ReorderSubjects(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetCurriculumId(), req.GetIds()); err != nil {
		return nil, s.fail("ReorderSubjects", err)
	}
	return &academicv1.ReorderSubjectsResponse{}, nil
}

// ---------- competencies ----------

func (s *curriculumServer) CreateCompetency(ctx context.Context, req *academicv1.CreateCompetencyRequest) (*academicv1.CreateCompetencyResponse, error) {
	c, err := s.curricula.CreateCompetency(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetCurriculumId(), domain.CompetencyInput{SubjectID: req.GetSubjectId(), Code: req.GetCode(), Name: req.GetName()})
	if err != nil {
		return nil, s.fail("CreateCompetency", err)
	}
	return &academicv1.CreateCompetencyResponse{Competency: toCompetency(c)}, nil
}

func (s *curriculumServer) UpdateCompetency(ctx context.Context, req *academicv1.UpdateCompetencyRequest) (*academicv1.UpdateCompetencyResponse, error) {
	c, err := s.curricula.UpdateCompetency(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), domain.CompetencyInput{Code: req.GetCode(), Name: req.GetName()})
	if err != nil {
		return nil, s.fail("UpdateCompetency", err)
	}
	return &academicv1.UpdateCompetencyResponse{Competency: toCompetency(c)}, nil
}

func (s *curriculumServer) DeleteCompetency(ctx context.Context, req *academicv1.DeleteCompetencyRequest) (*academicv1.DeleteCompetencyResponse, error) {
	if err := s.curricula.DeleteCompetency(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteCompetency", err)
	}
	return &academicv1.DeleteCompetencyResponse{}, nil
}

func (s *curriculumServer) ReorderCompetencies(ctx context.Context, req *academicv1.ReorderCompetenciesRequest) (*academicv1.ReorderCompetenciesResponse, error) {
	if err := s.curricula.ReorderCompetencies(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetCurriculumId(), req.GetSubjectId(), req.GetIds()); err != nil {
		return nil, s.fail("ReorderCompetencies", err)
	}
	return &academicv1.ReorderCompetenciesResponse{}, nil
}
