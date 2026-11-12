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

const dateLayout = "2006-01-02"

// academicServer exposes service.AcademicService to joseph-app's server
type academicServer struct {
	academicv1.UnimplementedAcademicServiceServer
	academic *service.AcademicService
	logger   zerolog.Logger
}

func (s *academicServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("academic grpc call failed")
	}
	return fromDomain(err)
}

// ---------- mapping ----------

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateLayout)
}

// parseDates reads YYYY-MM-DD values; a malformed one is a field error under its own name
func parseDates(values map[string]string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	fields := map[string]string{}
	for field, v := range values {
		if v == "" {
			out[field] = time.Time{}
			continue
		}
		t, err := time.Parse(dateLayout, v)
		if err != nil {
			fields[field] = "Fecha no válida."
			continue
		}
		out[field] = t
	}
	if len(fields) > 0 {
		return nil, domain.Validation(fields)
	}
	return out, nil
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	x := int32(*v)
	return &x
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	x := int(*v)
	return &x
}

func toContext(c service.AcademicContext) *academicv1.AcademicContext {
	v := c.Vocabulary
	st := c.Settings
	management := service.ManagementOf(st)
	return &academicv1.AcademicContext{
		InstitutionSlug: c.Institution.Slug, InstitutionName: c.Institution.Name, InstitutionKind: c.Institution.Kind,
		Vocabulary: &academicv1.Vocabulary{Level: v.Level, Levels: v.Levels, Grade: v.Grade, Grades: v.Grades, Section: v.Section, Sections: v.Sections, Period: v.Period, Periods: v.Periods},
		PeriodKind: c.PeriodKind, TermSchemes: c.TermSchemes, HasGrades: c.HasGrades, UsesAttentionModes: c.UsesAttentionModes,
		UsesSeasons: c.UsesSeasons, UsesFormativeLevels: c.UsesFormativeLevels, SingleActivePeriod: c.SingleActivePeriod, CanManage: c.CanManage,
		Settings: &academicv1.Settings{TermScheme: st.TermScheme, GradingScale: st.GradingScale, PassingGrade: int32Ptr(st.PassingGrade), ConductGrade: st.ConductGrade, AttentionModes: st.AttentionModes, Shifts: st.Shifts, WeekDays: toInt32s(st.WeekDays), LateAfterMinutes: int32(st.LateAfterMinutes), AbsenceLimitPercent: int32(st.AbsenceLimitPercent),
			Dre: &st.Dre, Ugel: &st.Ugel, ModularCode: &st.ModularCode, Management: &management},
	}
}

// withLogo adds the institution's logo to a context; only the structure screens (where it is edited) carry it,
// so every other page does not download the image
func withLogo(ctx *academicv1.AcademicContext, c service.AcademicContext) *academicv1.AcademicContext {
	logo := c.Settings.Logo
	ctx.Settings.Logo = &logo
	return ctx
}

func toGrade(g domain.Grade) *academicv1.Grade {
	return &academicv1.Grade{Id: g.ID, LevelId: g.LevelID, Code: g.Code, Name: g.Name, ShortName: g.ShortName, Position: int32(g.Position), SectionCount: int32(g.SectionCount)}
}

func toLevel(l domain.Level) *academicv1.Level {
	grades := make([]*academicv1.Grade, len(l.Grades))
	for i, g := range l.Grades {
		grades[i] = toGrade(g)
	}
	return &academicv1.Level{
		Id: l.ID, Code: l.Code, Name: l.Name, Description: l.Description, Position: int32(l.Position), FormativeLevel: l.FormativeLevel,
		Credits: int32Ptr(l.Credits), AttentionModes: l.AttentionModes, IsActive: l.IsActive, Grades: grades, SectionCount: int32(l.SectionCount),
	}
}

func toLevels(levels []domain.Level) []*academicv1.Level {
	out := make([]*academicv1.Level, len(levels))
	for i, l := range levels {
		out[i] = toLevel(l)
	}
	return out
}

func toTerm(t domain.Term) *academicv1.Term {
	return &academicv1.Term{Id: t.ID, Number: int32(t.Number), Name: t.Name, StartsOn: formatDate(t.StartsOn), EndsOn: formatDate(t.EndsOn), Status: t.Status}
}

func toPeriod(p domain.Period) *academicv1.Period {
	terms := make([]*academicv1.Term, len(p.Terms))
	for i, t := range p.Terms {
		terms[i] = toTerm(t)
	}
	return &academicv1.Period{
		Id: p.ID, Kind: p.Kind, Name: p.Name, Season: p.Season, StartsOn: formatDate(p.StartsOn), EndsOn: formatDate(p.EndsOn),
		TermScheme: p.TermScheme, Status: p.Status, Terms: terms, SectionCount: int32(p.SectionCount), CapacityTotal: int32(p.CapacityTotal),
	}
}

func toPeriods(periods []domain.Period) []*academicv1.Period {
	out := make([]*academicv1.Period, len(periods))
	for i, p := range periods {
		out[i] = toPeriod(p)
	}
	return out
}

func toSection(s domain.Section) *academicv1.Section {
	return &academicv1.Section{
		Id: s.ID, PeriodId: s.PeriodID, LevelId: s.LevelID, LevelName: s.LevelName, GradeId: s.GradeID, GradeName: s.GradeName,
		GradeShort: s.GradeShort, Name: s.Name, Shift: s.Shift, AttentionMode: s.AttentionMode, Capacity: int32(s.Capacity), Room: s.Room,
		TutorId: s.TutorID, TutorName: s.TutorName,
	}
}

func toSections(sections []domain.Section) []*academicv1.Section {
	out := make([]*academicv1.Section, len(sections))
	for i, x := range sections {
		out[i] = toSection(x)
	}
	return out
}

func levelInput(in *academicv1.LevelInput) domain.LevelInput {
	return domain.LevelInput{
		Code: in.GetCode(), Name: in.GetName(), Description: in.GetDescription(), FormativeLevel: in.GetFormativeLevel(),
		Credits: intPtr(in.Credits), AttentionModes: in.GetAttentionModes(), IsActive: in.GetIsActive(),
	}
}

func gradeInput(in *academicv1.GradeInput) domain.GradeInput {
	return domain.GradeInput{Code: in.GetCode(), Name: in.GetName(), ShortName: in.GetShortName()}
}

func periodInput(in *academicv1.PeriodInput) (domain.PeriodInput, error) {
	dates, err := parseDates(map[string]string{"startsOn": in.GetStartsOn(), "endsOn": in.GetEndsOn()})
	if err != nil {
		return domain.PeriodInput{}, err
	}
	return domain.PeriodInput{Name: in.GetName(), Season: in.GetSeason(), StartsOn: dates["startsOn"], EndsOn: dates["endsOn"], TermScheme: in.GetTermScheme()}, nil
}

func sectionInput(in *academicv1.SectionInput) domain.SectionInput {
	return domain.SectionInput{Name: in.GetName(), Shift: in.GetShift(), AttentionMode: in.GetAttentionMode(), Capacity: int(in.GetCapacity()), Room: in.GetRoom()}
}

// ---------- profile ----------

func (s *academicServer) GetContext(ctx context.Context, req *academicv1.GetContextRequest) (*academicv1.GetContextResponse, error) {
	c, err := s.academic.Context(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetContext", err)
	}
	return &academicv1.GetContextResponse{Context: toContext(c)}, nil
}

func (s *academicServer) UpdateSettings(ctx context.Context, req *academicv1.UpdateSettingsRequest) (*academicv1.UpdateSettingsResponse, error) {
	in := req.GetSettings()
	c, err := s.academic.UpdateSettings(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.AcademicSettings{
		TermScheme: in.GetTermScheme(), PassingGrade: intPtr(in.PassingGrade), ConductGrade: in.GetConductGrade(), AttentionModes: in.GetAttentionModes(), Shifts: in.GetShifts(), WeekDays: toInts(in.GetWeekDays()), LateAfterMinutes: int(in.GetLateAfterMinutes()), AbsenceLimitPercent: int(in.GetAbsenceLimitPercent()),
	}, service.ProfileInput{Dre: in.Dre, Ugel: in.Ugel, ModularCode: in.ModularCode, Logo: in.Logo, Management: in.Management})
	if err != nil {
		return nil, s.fail("UpdateSettings", err)
	}
	return &academicv1.UpdateSettingsResponse{Context: withLogo(toContext(c), c)}, nil
}

func (s *academicServer) GetManagement(ctx context.Context, req *academicv1.GetManagementRequest) (*academicv1.GetManagementResponse, error) {
	inst, management, err := s.academic.Management(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetManagement", err)
	}
	return &academicv1.GetManagementResponse{InstitutionSlug: inst.Slug, Management: management}, nil
}

// ---------- structure ----------

func (s *academicServer) GetStructure(ctx context.Context, req *academicv1.GetStructureRequest) (*academicv1.GetStructureResponse, error) {
	c, levels, err := s.academic.Structure(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetStructure", err)
	}
	return &academicv1.GetStructureResponse{Context: withLogo(toContext(c), c), Levels: toLevels(levels)}, nil
}

func (s *academicServer) CreateLevel(ctx context.Context, req *academicv1.CreateLevelRequest) (*academicv1.CreateLevelResponse, error) {
	l, err := s.academic.CreateLevel(ctx, bearer(ctx), req.GetInstitutionSlug(), levelInput(req.GetLevel()))
	if err != nil {
		return nil, s.fail("CreateLevel", err)
	}
	return &academicv1.CreateLevelResponse{Level: toLevel(l)}, nil
}

func (s *academicServer) UpdateLevel(ctx context.Context, req *academicv1.UpdateLevelRequest) (*academicv1.UpdateLevelResponse, error) {
	l, err := s.academic.UpdateLevel(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), levelInput(req.GetLevel()))
	if err != nil {
		return nil, s.fail("UpdateLevel", err)
	}
	return &academicv1.UpdateLevelResponse{Level: toLevel(l)}, nil
}

func (s *academicServer) DeleteLevel(ctx context.Context, req *academicv1.DeleteLevelRequest) (*academicv1.DeleteLevelResponse, error) {
	if err := s.academic.DeleteLevel(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteLevel", err)
	}
	return &academicv1.DeleteLevelResponse{}, nil
}

func (s *academicServer) ReorderLevels(ctx context.Context, req *academicv1.ReorderLevelsRequest) (*academicv1.ReorderLevelsResponse, error) {
	if err := s.academic.ReorderLevels(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetIds()); err != nil {
		return nil, s.fail("ReorderLevels", err)
	}
	return &academicv1.ReorderLevelsResponse{}, nil
}

func (s *academicServer) CreateGrade(ctx context.Context, req *academicv1.CreateGradeRequest) (*academicv1.CreateGradeResponse, error) {
	g, err := s.academic.CreateGrade(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetLevelId(), gradeInput(req.GetGrade()))
	if err != nil {
		return nil, s.fail("CreateGrade", err)
	}
	return &academicv1.CreateGradeResponse{Grade: toGrade(g)}, nil
}

func (s *academicServer) UpdateGrade(ctx context.Context, req *academicv1.UpdateGradeRequest) (*academicv1.UpdateGradeResponse, error) {
	g, err := s.academic.UpdateGrade(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), gradeInput(req.GetGrade()))
	if err != nil {
		return nil, s.fail("UpdateGrade", err)
	}
	return &academicv1.UpdateGradeResponse{Grade: toGrade(g)}, nil
}

func (s *academicServer) DeleteGrade(ctx context.Context, req *academicv1.DeleteGradeRequest) (*academicv1.DeleteGradeResponse, error) {
	if err := s.academic.DeleteGrade(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteGrade", err)
	}
	return &academicv1.DeleteGradeResponse{}, nil
}

func (s *academicServer) ReorderGrades(ctx context.Context, req *academicv1.ReorderGradesRequest) (*academicv1.ReorderGradesResponse, error) {
	if err := s.academic.ReorderGrades(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetLevelId(), req.GetIds()); err != nil {
		return nil, s.fail("ReorderGrades", err)
	}
	return &academicv1.ReorderGradesResponse{}, nil
}

func (s *academicServer) ApplyTemplate(ctx context.Context, req *academicv1.ApplyTemplateRequest) (*academicv1.ApplyTemplateResponse, error) {
	levels, err := s.academic.ApplyTemplate(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ApplyTemplate", err)
	}
	return &academicv1.ApplyTemplateResponse{Levels: toLevels(levels)}, nil
}

// ---------- periods ----------

func (s *academicServer) ListPeriods(ctx context.Context, req *academicv1.ListPeriodsRequest) (*academicv1.ListPeriodsResponse, error) {
	c, periods, err := s.academic.Periods(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListPeriods", err)
	}
	return &academicv1.ListPeriodsResponse{Context: toContext(c), Periods: toPeriods(periods)}, nil
}

func (s *academicServer) GetPeriod(ctx context.Context, req *academicv1.GetPeriodRequest) (*academicv1.GetPeriodResponse, error) {
	token := bearer(ctx)
	d, err := s.academic.Period(ctx, token, req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetPeriod", err)
	}
	_, periods, err := s.academic.Periods(ctx, token, req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetPeriod", err)
	}
	var others []domain.Period
	for _, p := range periods {
		if p.ID != d.Period.ID && p.SectionCount > 0 {
			others = append(others, p)
		}
	}
	return &academicv1.GetPeriodResponse{Context: toContext(d.Context), Period: toPeriod(d.Period), Sections: toSections(d.Sections), Levels: toLevels(d.Levels), OtherPeriods: toPeriods(others)}, nil
}

func (s *academicServer) CreatePeriod(ctx context.Context, req *academicv1.CreatePeriodRequest) (*academicv1.CreatePeriodResponse, error) {
	in, err := periodInput(req.GetPeriod())
	if err != nil {
		return nil, s.fail("CreatePeriod", err)
	}
	p, err := s.academic.CreatePeriod(ctx, bearer(ctx), req.GetInstitutionSlug(), in)
	if err != nil {
		return nil, s.fail("CreatePeriod", err)
	}
	return &academicv1.CreatePeriodResponse{Period: toPeriod(p)}, nil
}

func (s *academicServer) UpdatePeriod(ctx context.Context, req *academicv1.UpdatePeriodRequest) (*academicv1.UpdatePeriodResponse, error) {
	in, err := periodInput(req.GetPeriod())
	if err != nil {
		return nil, s.fail("UpdatePeriod", err)
	}
	p, err := s.academic.UpdatePeriod(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), in)
	if err != nil {
		return nil, s.fail("UpdatePeriod", err)
	}
	return &academicv1.UpdatePeriodResponse{Period: toPeriod(p)}, nil
}

func (s *academicServer) SetPeriodStatus(ctx context.Context, req *academicv1.SetPeriodStatusRequest) (*academicv1.SetPeriodStatusResponse, error) {
	p, err := s.academic.SetPeriodStatus(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus())
	if err != nil {
		return nil, s.fail("SetPeriodStatus", err)
	}
	return &academicv1.SetPeriodStatusResponse{Period: toPeriod(p)}, nil
}

func (s *academicServer) DeletePeriod(ctx context.Context, req *academicv1.DeletePeriodRequest) (*academicv1.DeletePeriodResponse, error) {
	if err := s.academic.DeletePeriod(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeletePeriod", err)
	}
	return &academicv1.DeletePeriodResponse{}, nil
}

func (s *academicServer) UpdateTerm(ctx context.Context, req *academicv1.UpdateTermRequest) (*academicv1.UpdateTermResponse, error) {
	dates, err := parseDates(map[string]string{"startsOn": req.GetStartsOn(), "endsOn": req.GetEndsOn()})
	if err != nil {
		return nil, s.fail("UpdateTerm", err)
	}
	t, err := s.academic.UpdateTerm(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), req.GetId(),
		domain.TermInput{Name: req.GetName(), StartsOn: dates["startsOn"], EndsOn: dates["endsOn"], Status: req.GetStatus()})
	if err != nil {
		return nil, s.fail("UpdateTerm", err)
	}
	return &academicv1.UpdateTermResponse{Term: toTerm(t)}, nil
}

// ---------- sections ----------

func (s *academicServer) CreateSections(ctx context.Context, req *academicv1.CreateSectionsRequest) (*academicv1.CreateSectionsResponse, error) {
	created, err := s.academic.CreateSections(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), domain.SectionBatch{
		LevelID: req.GetLevelId(), GradeIDs: req.GetGradeIds(), Names: req.GetNames(), Shift: req.GetShift(),
		AttentionMode: req.GetAttentionMode(), Capacity: int(req.GetCapacity()), Room: req.GetRoom(),
	})
	if err != nil {
		return nil, s.fail("CreateSections", err)
	}
	return &academicv1.CreateSectionsResponse{Sections: toSections(created)}, nil
}

func (s *academicServer) UpdateSection(ctx context.Context, req *academicv1.UpdateSectionRequest) (*academicv1.UpdateSectionResponse, error) {
	out, err := s.academic.UpdateSection(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), sectionInput(req.GetSection()))
	if err != nil {
		return nil, s.fail("UpdateSection", err)
	}
	return &academicv1.UpdateSectionResponse{Section: toSection(out)}, nil
}

func (s *academicServer) DeleteSection(ctx context.Context, req *academicv1.DeleteSectionRequest) (*academicv1.DeleteSectionResponse, error) {
	if err := s.academic.DeleteSection(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteSection", err)
	}
	return &academicv1.DeleteSectionResponse{}, nil
}

func (s *academicServer) CopySections(ctx context.Context, req *academicv1.CopySectionsRequest) (*academicv1.CopySectionsResponse, error) {
	added, err := s.academic.CopySections(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), req.GetFromPeriodId())
	if err != nil {
		return nil, s.fail("CopySections", err)
	}
	return &academicv1.CopySectionsResponse{Added: int32(added)}, nil
}

func toInt32s(values []int) []int32 {
	out := make([]int32, len(values))
	for i, v := range values {
		out[i] = int32(v)
	}
	return out
}

func toInts(values []int32) []int {
	out := make([]int, len(values))
	for i, v := range values {
		out[i] = int(v)
	}
	return out
}
