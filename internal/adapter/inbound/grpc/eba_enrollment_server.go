package grpc

import (
	"context"
	"fmt"
	"strconv"

	"api/internal/adapter/inbound/grpc/gen/documentsv1"
	"api/internal/adapter/inbound/grpc/gen/peoplev1"
	"api/internal/core/domain"
)

// EBA enrollment (phase E1) of the people and documents servers

func toEnrollmentFile(f domain.EnrollmentFile) *peoplev1.EnrollmentFile {
	return &peoplev1.EnrollmentFile{StudentId: f.StudentID, MotherTongue: f.MotherTongue, MotherTongueOther: f.MotherTongueOther, Occupation: f.Occupation,
		Disability: f.Disability, Internet: f.Internet, Device: f.Device, PreferredMode: f.PreferredMode, LastStudies: f.LastStudies, Documents: f.Documents,
		UpdatedByName: f.UpdatedByName, UpdatedAt: rfc3339(f.UpdatedAt)}
}

func (s *peopleServer) GetEnrollmentFile(ctx context.Context, req *peoplev1.GetEnrollmentFileRequest) (*peoplev1.GetEnrollmentFileResponse, error) {
	f, err := s.people.EnrollmentFile(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId())
	if err != nil {
		return nil, s.fail("GetEnrollmentFile", err)
	}
	return &peoplev1.GetEnrollmentFileResponse{File: toEnrollmentFile(f)}, nil
}

func (s *peopleServer) SaveEnrollmentFile(ctx context.Context, req *peoplev1.SaveEnrollmentFileRequest) (*peoplev1.SaveEnrollmentFileResponse, error) {
	in := req.GetFile()
	f, err := s.people.SaveEnrollmentFile(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.EnrollmentFile{StudentID: in.GetStudentId(), MotherTongue: in.GetMotherTongue(),
		MotherTongueOther: in.GetMotherTongueOther(), Occupation: in.GetOccupation(), Disability: in.GetDisability(), Internet: in.GetInternet(), Device: in.GetDevice(),
		PreferredMode: in.GetPreferredMode(), LastStudies: in.GetLastStudies(), Documents: in.GetDocuments()})
	if err != nil {
		return nil, s.fail("SaveEnrollmentFile", err)
	}
	return &peoplev1.SaveEnrollmentFileResponse{File: toEnrollmentFile(f)}, nil
}

func (s *peopleServer) SetReligionExemption(ctx context.Context, req *peoplev1.SetReligionExemptionRequest) (*peoplev1.SetReligionExemptionResponse, error) {
	on, err := requiredDate("requestedOn", req.GetRequestedOn())
	if err != nil {
		return nil, s.fail("SetReligionExemption", err)
	}
	if err := s.people.SetReligionExemption(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetEnrollmentId(), req.GetExempt(), on, req.GetReference()); err != nil {
		return nil, s.fail("SetReligionExemption", err)
	}
	return &peoplev1.SetReligionExemptionResponse{}, nil
}

func rollFilter(f *peoplev1.RollFilter) domain.RollFilter {
	return domain.RollFilter{PeriodID: f.GetPeriodId(), LevelID: f.GetLevelId(), GradeID: f.GetGradeId(), SectionID: f.GetSectionId(), SiagieStatus: f.GetSiagieStatus(), All: f.GetAll()}
}

func rollQuery(f *documentsv1.RollQuery) domain.RollFilter {
	return domain.RollFilter{PeriodID: f.GetPeriodId(), LevelID: f.GetLevelId(), GradeID: f.GetGradeId(), SectionID: f.GetSectionId(), SiagieStatus: f.GetSiagieStatus(), All: f.GetAll()}
}

func (s *peopleServer) ListRoll(ctx context.Context, req *peoplev1.ListRollRequest) (*peoplev1.ListRollResponse, error) {
	v, err := s.people.Roll(ctx, bearer(ctx), req.GetInstitutionSlug(), rollFilter(req.GetFilter()))
	if err != nil {
		return nil, s.fail("ListRoll", err)
	}
	out := &peoplev1.ListRollResponse{Pending: int32(v.Pending), Exported: int32(v.Exported), Observed: int32(v.Observed), Exempt: int32(v.Exempt), CanManage: v.CanManage}
	for _, r := range v.Rows {
		name := r.PaternalSurname
		if r.MaternalSurname != "" {
			name += " " + r.MaternalSurname
		}
		out.Rows = append(out.Rows, &peoplev1.RollRow{EnrollmentId: r.EnrollmentID, StudentId: r.StudentID, StudentCode: r.StudentCode, DocumentType: r.DocumentType,
			DocumentNumber: r.DocumentNumber, StudentName: name + ", " + r.GivenNames, Sex: r.Sex, BirthDate: datePtr(r.BirthDate), LevelId: r.LevelID, LevelName: r.LevelName,
			GradeId: r.GradeID, GradeName: r.GradeName, SectionId: r.SectionID, SectionName: r.SectionName, AttentionMode: r.AttentionMode, Kind: r.Kind, Status: r.Status,
			EnrolledOn: formatDate(r.EnrolledOn), ReligionExempt: r.ReligionExempt, SiagieStatus: r.SiagieStatus, SiagieNote: r.SiagieNote,
			SiagieExportedAt: rfc3339(r.SiagieExported), Placement: r.Placement})
	}
	return out, nil
}

func (s *peopleServer) SetSiagieStatus(ctx context.Context, req *peoplev1.SetSiagieStatusRequest) (*peoplev1.SetSiagieStatusResponse, error) {
	n, err := s.people.SetSiagie(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetEnrollmentIds(), req.GetStatus(), req.GetNote())
	if err != nil {
		return nil, s.fail("SetSiagieStatus", err)
	}
	return &peoplev1.SetSiagieStatusResponse{Updated: int32(n)}, nil
}

func (s *peopleServer) ProposePlacement(ctx context.Context, req *peoplev1.ProposePlacementRequest) (*peoplev1.ProposePlacementResponse, error) {
	p, err := s.people.ProposePlacement(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetOrigin(), int(req.GetPassed()))
	if err != nil {
		return nil, s.fail("ProposePlacement", err)
	}
	return &peoplev1.ProposePlacementResponse{LevelId: p.LevelID, LevelName: p.LevelName, GradeId: p.GradeID, GradeName: p.GradeName, Done: p.Done}, nil
}

// ---------- documents ----------

func (s *documentsServer) EnrollmentFormPdf(ctx context.Context, req *documentsv1.EnrollmentFormPdfRequest) (*documentsv1.EnrollmentFormPdfResponse, error) {
	f, err := s.documents.EnrollmentFormPdf(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetPeriodId())
	file, err := s.reply("EnrollmentFormPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.EnrollmentFormPdfResponse{File: file}, nil
}

func (s *documentsServer) RollPdf(ctx context.Context, req *documentsv1.RollPdfRequest) (*documentsv1.RollPdfResponse, error) {
	f, err := s.documents.RollPdf(ctx, bearer(ctx), req.GetInstitutionSlug(), rollQuery(req.GetFilter()))
	file, err := s.reply("RollPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.RollPdfResponse{File: file}, nil
}

func (s *documentsServer) RollXlsx(ctx context.Context, req *documentsv1.RollXlsxRequest) (*documentsv1.RollXlsxResponse, error) {
	f, err := s.documents.RollXlsx(ctx, bearer(ctx), req.GetInstitutionSlug(), rollQuery(req.GetFilter()))
	file, err := s.reply("RollXlsx", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.RollXlsxResponse{File: file}, nil
}

func (s *documentsServer) SiagieXlsx(ctx context.Context, req *documentsv1.SiagieXlsxRequest) (*documentsv1.SiagieXlsxResponse, error) {
	f, marked, err := s.documents.SiagieXlsx(ctx, bearer(ctx), req.GetInstitutionSlug(), rollQuery(req.GetFilter()), req.GetMarkExported())
	file, err := s.reply("SiagieXlsx", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.SiagieXlsxResponse{File: file, Marked: int32(marked)}, nil
}

func (s *documentsServer) GeneratePlacementAct(ctx context.Context, req *documentsv1.GeneratePlacementActRequest) (*documentsv1.GeneratePlacementActResponse, error) {
	p, v, err := s.documents.GeneratePlacementAct(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPlacementId(), req.GetNote())
	if err != nil {
		return nil, s.fail("GeneratePlacementAct", err)
	}
	return &documentsv1.GeneratePlacementActResponse{Version: versionMsg(v), ActCode: actCodeOf(p)}, nil
}

func actCodeOf(p domain.Placement) string {
	if p.ActNumber == 0 {
		return ""
	}
	return "N.° " + pad3(p.ActNumber) + "-" + itoa(p.ActYear)
}

func (s *documentsServer) PlacementActPdf(ctx context.Context, req *documentsv1.PlacementActPdfRequest) (*documentsv1.PlacementActPdfResponse, error) {
	f, err := s.documents.PlacementActPdf(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPlacementId())
	file, err := s.reply("PlacementActPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.PlacementActPdfResponse{File: file}, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

func pad3(n int) string { return fmt.Sprintf("%03d", n) }
