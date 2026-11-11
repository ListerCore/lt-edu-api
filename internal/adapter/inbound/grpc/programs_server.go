package grpc

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/programsv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// programsServer exposes service.ProgramsService to joseph-app's server
type programsServer struct {
	programsv1.UnimplementedProgramsServiceServer
	programs *service.ProgramsService
	logger   zerolog.Logger
}

func (s *programsServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("programs grpc call failed")
	}
	return fromDomain(err)
}

func toInternship(x domain.Internship) *programsv1.Internship {
	return &programsv1.Internship{Id: x.ID, StudentId: x.StudentID, StudentName: x.StudentName, StudentCode: x.StudentCode, SubjectId: x.SubjectID, SubjectName: x.SubjectName,
		ModuleName: x.ModuleName, LevelId: x.LevelID, ProgramName: x.ProgramName, Company: x.Company, CompanyRuc: x.CompanyRUC, Area: x.Area, Supervisor: x.Supervisor,
		SupervisorContact: x.SupervisorContact, StartsOn: formatDate(x.StartsOn), EndsOn: datePtr(x.EndsOn), RequiredHours: int32(x.RequiredHours), LoggedHours: x.LoggedHours,
		Status: x.Status, Score: int32Ptr(x.Score), Evaluation: x.Evaluation, EvaluatedName: x.EvaluatedName, EvaluatedAt: instant(x.EvaluatedAt)}
}

func toCertificate(c domain.Certificate) *programsv1.Certificate {
	return &programsv1.Certificate{Id: c.ID, StudentId: c.StudentID, StudentName: c.StudentName, StudentCode: c.StudentCode, LevelId: c.LevelID, ProgramName: c.ProgramName,
		Kind: c.Kind, ModuleId: c.ModuleID, ModuleName: c.ModuleName, Code: c.Code, Status: c.Status, IssuedOn: formatDate(c.IssuedOn), IssuedName: c.IssuedName, AnnulReason: c.AnnulReason}
}

func toProgress(p service.StudentProgress) *programsv1.StudentProgress {
	return &programsv1.StudentProgress{Id: p.ID, Code: p.Code, Name: p.Name, Document: p.Document, Status: p.Status, Semester: p.Semester, ApprovedCredits: int32(p.Approved),
		TotalCredits: int32(p.Total), Modules: int32(p.Modules), Certified: int32(p.Certified), Degree: p.Degree, EfsrtDone: int32(p.EFSRTDone), EfsrtTotal: int32(p.EFSRTTotal)}
}

func toRecord(r service.StudentRecord) *programsv1.StudentRecord {
	out := &programsv1.StudentRecord{Student: toProgress(service.StudentProgress{ProgramStudent: r.Student}), ProgramName: r.Program.Name, LevelId: r.Student.LevelID,
		Eligible: r.Eligible, Approved: int32(r.Approved), Total: int32(r.Total), CanManage: r.CanManage, CanIssue: r.CanIssue, PassingGrade: int32(r.Passing)}
	if r.Degree != nil {
		out.Degree = toCertificate(*r.Degree)
	}
	for _, c := range r.Certificates {
		out.Certificates = append(out.Certificates, toCertificate(c))
	}
	for _, m := range r.Modules {
		rm := &programsv1.RecordModule{Id: m.Module.ID, Name: m.Module.Name, Complete: m.Complete, Credits: int32(m.Credits), Approved: int32(m.Approved)}
		if m.Certificate != nil {
			rm.Certificate = toCertificate(*m.Certificate)
		}
		for _, u := range m.Units {
			ru := &programsv1.RecordUnit{SubjectId: u.SubjectID, Kind: u.Kind, Name: u.Name, Credits: int32(u.Credits()), Semester: u.Semester, Hours: int32(u.Hours)}
			if u.Standing != nil {
				ru.Standing = &programsv1.Standing{Status: u.Standing.Status, Grade: int32Ptr(u.Standing.Grade), PeriodName: u.Standing.PeriodName, Source: u.Standing.Source}
			}
			for _, i := range u.Internships {
				ru.Internships = append(ru.Internships, toInternship(i))
			}
			rm.Units = append(rm.Units, ru)
		}
		out.Modules = append(out.Modules, rm)
	}
	for _, q := range r.Requirements {
		out.Requirements = append(out.Requirements, &programsv1.Requirement{Code: q.Code, Name: q.Name, Auto: q.Auto, Done: q.Done, Note: q.Note, CheckedName: q.Doer, CheckedAt: instant(q.DoneAt)})
	}
	return out
}

func toInternshipDetail(d service.InternshipDetail) *programsv1.GetInternshipResponse {
	out := &programsv1.GetInternshipResponse{Internship: toInternship(d.Internship), CanEdit: d.CanEdit, PassingGrade: int32(d.Passing)}
	for _, l := range d.Logs {
		out.Logs = append(out.Logs, &programsv1.InternshipLog{Id: l.ID, Day: formatDate(l.Day), Hours: l.Hours, Activity: l.Activity})
	}
	return out
}

func (s *programsServer) GetPrograms(ctx context.Context, req *programsv1.GetProgramsRequest) (*programsv1.GetProgramsResponse, error) {
	o, err := s.programs.Overview(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetPrograms", err)
	}
	out := &programsv1.GetProgramsResponse{CanAssign: o.CanAssign, Staff: toStaffList(o.Teachers)}
	for _, p := range o.Programs {
		out.Programs = append(out.Programs, &programsv1.Program{LevelId: p.LevelID, Code: p.Code, Name: p.Name, CoordinatorId: p.CoordinatorID, CoordinatorName: p.CoordinatorName,
			Students: int32(p.Students), Ongoing: int32(p.Ongoing), ToEvaluate: int32(p.ToEvaluate), Modulars: int32(p.Modulars), Degrees: int32(p.Degrees), CanManage: p.CanManage,
			Modules: int32(p.Modules), TotalCredits: int32(p.TotalCredit)})
	}
	return out, nil
}

func (s *programsServer) SetCoordinator(ctx context.Context, req *programsv1.SetCoordinatorRequest) (*programsv1.SetCoordinatorResponse, error) {
	if err := s.programs.SetCoordinator(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetLevelId(), req.GetStaffId()); err != nil {
		return nil, s.fail("SetCoordinator", err)
	}
	return &programsv1.SetCoordinatorResponse{}, nil
}

func (s *programsServer) ListStudents(ctx context.Context, req *programsv1.ListStudentsRequest) (*programsv1.ListStudentsResponse, error) {
	list, err := s.programs.Students(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetLevelId(), req.GetQuery())
	if err != nil {
		return nil, s.fail("ListStudents", err)
	}
	out := &programsv1.ListStudentsResponse{}
	for _, p := range list {
		out.Students = append(out.Students, toProgress(p))
	}
	return out, nil
}

func (s *programsServer) GetRecord(ctx context.Context, req *programsv1.GetRecordRequest) (*programsv1.GetRecordResponse, error) {
	r, err := s.programs.Record(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId())
	if err != nil {
		return nil, s.fail("GetRecord", err)
	}
	return &programsv1.GetRecordResponse{Record: toRecord(r)}, nil
}

func (s *programsServer) MyRecords(ctx context.Context, req *programsv1.MyRecordsRequest) (*programsv1.MyRecordsResponse, error) {
	list, err := s.programs.MyRecords(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("MyRecords", err)
	}
	out := &programsv1.MyRecordsResponse{}
	for _, r := range list {
		out.Records = append(out.Records, toRecord(r))
	}
	return out, nil
}

func (s *programsServer) ListInternships(ctx context.Context, req *programsv1.ListInternshipsRequest) (*programsv1.ListInternshipsResponse, error) {
	list, can, err := s.programs.Internships(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetLevelId(), req.GetStatus())
	if err != nil {
		return nil, s.fail("ListInternships", err)
	}
	out := &programsv1.ListInternshipsResponse{CanManage: can}
	for _, x := range list {
		out.Internships = append(out.Internships, toInternship(x))
	}
	return out, nil
}

func (s *programsServer) GetInternship(ctx context.Context, req *programsv1.GetInternshipRequest) (*programsv1.GetInternshipResponse, error) {
	d, err := s.programs.Internship(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetInternship", err)
	}
	return toInternshipDetail(d), nil
}

func (s *programsServer) SaveInternship(ctx context.Context, req *programsv1.SaveInternshipRequest) (*programsv1.SaveInternshipResponse, error) {
	in := req.GetInternship()
	start, err := optionalDate("startsOn", in.GetStartsOn())
	if err != nil {
		return nil, err
	}
	end, err := optionalDate("endsOn", in.GetEndsOn())
	if err != nil {
		return nil, err
	}
	x := domain.Internship{ID: in.GetId(), StudentID: in.GetStudentId(), SubjectID: in.GetSubjectId(), Company: in.GetCompany(), CompanyRUC: in.GetCompanyRuc(), Area: in.GetArea(),
		Supervisor: in.GetSupervisor(), SupervisorContact: in.GetSupervisorContact(), EndsOn: end}
	if start != nil {
		x.StartsOn = *start
	}
	out, err := s.programs.SaveInternship(ctx, bearer(ctx), req.GetInstitutionSlug(), x)
	if err != nil {
		return nil, s.fail("SaveInternship", err)
	}
	return &programsv1.SaveInternshipResponse{Internship: toInternship(out)}, nil
}

func (s *programsServer) DeleteInternship(ctx context.Context, req *programsv1.DeleteInternshipRequest) (*programsv1.DeleteInternshipResponse, error) {
	if err := s.programs.DeleteInternship(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteInternship", err)
	}
	return &programsv1.DeleteInternshipResponse{}, nil
}

func (s *programsServer) AddLog(ctx context.Context, req *programsv1.AddLogRequest) (*programsv1.AddLogResponse, error) {
	l := req.GetLog()
	day, err := optionalDate("day", l.GetDay())
	if err != nil {
		return nil, err
	}
	in := domain.InternshipLog{Hours: l.GetHours(), Activity: l.GetActivity()}
	if day != nil {
		in.Day = *day
	}
	d, err := s.programs.AddLog(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), in)
	if err != nil {
		return nil, s.fail("AddLog", err)
	}
	return &programsv1.AddLogResponse{Internship: toInternshipDetail(d)}, nil
}

func (s *programsServer) DeleteLog(ctx context.Context, req *programsv1.DeleteLogRequest) (*programsv1.DeleteLogResponse, error) {
	d, err := s.programs.DeleteLog(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetLogId())
	if err != nil {
		return nil, s.fail("DeleteLog", err)
	}
	return &programsv1.DeleteLogResponse{Internship: toInternshipDetail(d)}, nil
}

func (s *programsServer) EvaluateInternship(ctx context.Context, req *programsv1.EvaluateInternshipRequest) (*programsv1.EvaluateInternshipResponse, error) {
	d, err := s.programs.Evaluate(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), int(req.GetScore()), req.GetEvaluation())
	if err != nil {
		return nil, s.fail("EvaluateInternship", err)
	}
	return &programsv1.EvaluateInternshipResponse{Internship: toInternshipDetail(d)}, nil
}

func (s *programsServer) IssueModular(ctx context.Context, req *programsv1.IssueModularRequest) (*programsv1.IssueModularResponse, error) {
	c, err := s.programs.IssueModular(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetModuleId())
	if err != nil {
		return nil, s.fail("IssueModular", err)
	}
	return &programsv1.IssueModularResponse{Certificate: toCertificate(c)}, nil
}

func (s *programsServer) IssueDegree(ctx context.Context, req *programsv1.IssueDegreeRequest) (*programsv1.IssueDegreeResponse, error) {
	c, err := s.programs.IssueDegree(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId())
	if err != nil {
		return nil, s.fail("IssueDegree", err)
	}
	return &programsv1.IssueDegreeResponse{Certificate: toCertificate(c)}, nil
}

func (s *programsServer) SetRequirement(ctx context.Context, req *programsv1.SetRequirementRequest) (*programsv1.SetRequirementResponse, error) {
	r, err := s.programs.SetRequirement(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetCode(), req.GetDone(), req.GetNote())
	if err != nil {
		return nil, s.fail("SetRequirement", err)
	}
	return &programsv1.SetRequirementResponse{Record: toRecord(r)}, nil
}

func (s *programsServer) ListCertificates(ctx context.Context, req *programsv1.ListCertificatesRequest) (*programsv1.ListCertificatesResponse, error) {
	list, can, err := s.programs.Certificates(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetLevelId(), req.GetKind())
	if err != nil {
		return nil, s.fail("ListCertificates", err)
	}
	out := &programsv1.ListCertificatesResponse{CanAnnul: can}
	for _, c := range list {
		out.Certificates = append(out.Certificates, toCertificate(c))
	}
	return out, nil
}

func (s *programsServer) GetCertificate(ctx context.Context, req *programsv1.GetCertificateRequest) (*programsv1.GetCertificateResponse, error) {
	c, err := s.programs.Certificate(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetCertificate", err)
	}
	return &programsv1.GetCertificateResponse{Certificate: toCertificate(c), Content: string(c.Content)}, nil
}

func (s *programsServer) AnnulCertificate(ctx context.Context, req *programsv1.AnnulCertificateRequest) (*programsv1.AnnulCertificateResponse, error) {
	if err := s.programs.Annul(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetReason()); err != nil {
		return nil, s.fail("AnnulCertificate", err)
	}
	return &programsv1.AnnulCertificateResponse{}, nil
}
