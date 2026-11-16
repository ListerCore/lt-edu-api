package grpc

import (
	"context"

	"api/internal/adapter/inbound/grpc/gen/assessmentv1"
	"api/internal/adapter/inbound/grpc/gen/documentsv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// Phase E7: promotion rules and SIAGIE state of the grades (closing), signed actas, the grades spreadsheet and the
// certificates (documents)

func toPromotionRules(r service.PromotionRules) []*assessmentv1.PromotionRule {
	out := make([]*assessmentv1.PromotionRule, 0, len(r.Rules))
	for _, x := range r.Rules {
		out = append(out, &assessmentv1.PromotionRule{LevelId: x.LevelID, LevelCode: x.LevelCode, LevelName: x.LevelName, Automatic: x.Automatic, Areas: int32(x.Areas),
			Note: x.Note, Custom: x.Custom, UpdatedName: x.UpdatedName, UpdatedAt: instant(x.UpdatedAt), DefaultName: x.DefaultName, DefaultAreas: int32(x.DefaultAreas),
			DefaultAutomatic: x.DefaultAutomatic})
	}
	return out
}

func (s *closingServer) GetPromotionRules(ctx context.Context, req *assessmentv1.GetPromotionRulesRequest) (*assessmentv1.GetPromotionRulesResponse, error) {
	r, err := s.closing.PromotionRules(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetPromotionRules", err)
	}
	return &assessmentv1.GetPromotionRulesResponse{Rules: toPromotionRules(r), CanManage: r.CanManage}, nil
}

func (s *closingServer) SavePromotionRule(ctx context.Context, req *assessmentv1.SavePromotionRuleRequest) (*assessmentv1.SavePromotionRuleResponse, error) {
	r, err := s.closing.SavePromotionRule(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.LevelRule{LevelID: req.GetLevelId(), Automatic: req.GetAutomatic(),
		Areas: int(req.GetAreas()), Note: req.GetNote()})
	if err != nil {
		return nil, s.fail("SavePromotionRule", err)
	}
	return &assessmentv1.SavePromotionRuleResponse{Rules: toPromotionRules(r), CanManage: r.CanManage}, nil
}

func (s *closingServer) ResetPromotionRule(ctx context.Context, req *assessmentv1.ResetPromotionRuleRequest) (*assessmentv1.ResetPromotionRuleResponse, error) {
	r, err := s.closing.ResetPromotionRule(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetLevelId())
	if err != nil {
		return nil, s.fail("ResetPromotionRule", err)
	}
	return &assessmentv1.ResetPromotionRuleResponse{Rules: toPromotionRules(r), CanManage: r.CanManage}, nil
}

func (s *closingServer) SetSiagieGrades(ctx context.Context, req *assessmentv1.SetSiagieGradesRequest) (*assessmentv1.SetSiagieGradesResponse, error) {
	r, err := s.closing.SetSiagieGrades(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetStudentIds(), req.GetStatus(), req.GetNote())
	if err != nil {
		return nil, s.fail("SetSiagieGrades", err)
	}
	return &assessmentv1.SetSiagieGradesResponse{Section: toSectionClosing(r)}, nil
}

// ---------- documents ----------

func (s *documentsServer) GenerateRecordAct(ctx context.Context, req *documentsv1.GenerateRecordActRequest) (*documentsv1.GenerateRecordActResponse, error) {
	_, v, err := s.documents.GenerateRecordAct(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetRecordId(), req.GetNote())
	if err != nil {
		return nil, s.fail("GenerateRecordAct", err)
	}
	return &documentsv1.GenerateRecordActResponse{Version: versionMsg(v)}, nil
}

func (s *documentsServer) RecordActPdf(ctx context.Context, req *documentsv1.RecordActPdfRequest) (*documentsv1.RecordActPdfResponse, error) {
	f, err := s.documents.RecordActPdf(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetRecordId())
	file, err := s.reply("RecordActPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.RecordActPdfResponse{File: file}, nil
}

func (s *documentsServer) SiagieGradesXlsx(ctx context.Context, req *documentsv1.SiagieGradesXlsxRequest) (*documentsv1.SiagieGradesXlsxResponse, error) {
	f, marked, err := s.documents.SiagieGradesXlsx(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetMarkExported())
	file, err := s.reply("SiagieGradesXlsx", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.SiagieGradesXlsxResponse{File: file, Marked: int32(marked)}, nil
}

func certificateMsg(c domain.StudentCertificate) *documentsv1.StudentCertificate {
	return &documentsv1.StudentCertificate{Id: c.ID, StudentId: c.StudentID, StudentName: c.StudentName, Kind: c.Kind, PeriodId: c.PeriodID, PeriodName: c.PeriodName,
		LevelId: c.LevelID, LevelName: c.LevelName, Purpose: c.Purpose, Year: int32(c.Year), Number: int32(c.Number), Code: "N.° " + pad3(c.Number) + "-" + itoa(c.Year),
		VersionId: c.VersionID, VersionStatus: c.VersionState, CreatedName: c.CreatedName, CreatedAt: instant(&c.CreatedAt)}
}

func studyMsgs(items []domain.StudyRecord) []*documentsv1.StudyRecord {
	out := make([]*documentsv1.StudyRecord, 0, len(items))
	for _, r := range items {
		out = append(out, &documentsv1.StudyRecord{EnrollmentId: r.EnrollmentID, PeriodId: r.PeriodID, PeriodName: r.PeriodName, Year: int32(r.Year), LevelId: r.LevelID,
			LevelName: r.LevelName, GradeName: r.GradeName, GradeShort: r.GradeShort, LastGrade: r.LastGrade, SectionName: r.SectionName, AttentionMode: r.AttentionMode,
			Status: r.Status, Situation: r.Situation, Confirmed: r.Confirmed})
	}
	return out
}

func (s *documentsServer) GetStudentCertificates(ctx context.Context, req *documentsv1.GetStudentCertificatesRequest) (*documentsv1.GetStudentCertificatesResponse, error) {
	b, err := s.documents.StudentCertificates(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId())
	if err != nil {
		return nil, s.fail("GetStudentCertificates", err)
	}
	out := &documentsv1.GetStudentCertificatesResponse{Studies: studyMsgs(b.Studies), Concluded: studyMsgs(b.Concluded), CanManage: b.CanManage}
	for _, c := range b.Certificates {
		out.Certificates = append(out.Certificates, certificateMsg(c))
	}
	return out, nil
}

func (s *documentsServer) GenerateCertificate(ctx context.Context, req *documentsv1.GenerateCertificateRequest) (*documentsv1.GenerateCertificateResponse, error) {
	c, v, err := s.documents.GenerateCertificate(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.StudentCertificateInput{StudentID: req.GetStudentId(), Kind: req.GetKind(),
		PeriodID: req.GetPeriodId(), LevelID: req.GetLevelId(), Purpose: req.GetPurpose()}, req.GetNote())
	if err != nil {
		return nil, s.fail("GenerateCertificate", err)
	}
	return &documentsv1.GenerateCertificateResponse{Certificate: certificateMsg(c), Version: versionMsg(v)}, nil
}

func (s *documentsServer) CertificatePdf(ctx context.Context, req *documentsv1.CertificatePdfRequest) (*documentsv1.CertificatePdfResponse, error) {
	f, err := s.documents.CertificatePdf(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetCertificateId())
	file, err := s.reply("CertificatePdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.CertificatePdfResponse{File: file}, nil
}
