package grpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/documentsv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// documentsServer exposes service.DocumentService to joseph-app's server
type documentsServer struct {
	documentsv1.UnimplementedDocumentsServiceServer
	documents *service.DocumentService
	logger    zerolog.Logger
}

func (s *documentsServer) reply(method string, f service.PdfFile, err error) (*documentsv1.PdfFile, error) {
	if err != nil {
		var de *domain.Error
		if !errors.As(err, &de) {
			s.logger.Error().Err(err).Str("method", method).Msg("documents grpc call failed")
		}
		return nil, fromDomain(err)
	}
	return &documentsv1.PdfFile{FileName: f.Name, Content: f.Content}, nil
}

func (s *documentsServer) ReportCardsPdf(ctx context.Context, req *documentsv1.ReportCardsPdfRequest) (*documentsv1.ReportCardsPdfResponse, error) {
	f, err := s.documents.ReportCards(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetTermId(), req.GetStudentId(), req.GetPeriodId())
	file, err := s.reply("ReportCardsPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.ReportCardsPdfResponse{File: file}, nil
}

func (s *documentsServer) SchedulesPdf(ctx context.Context, req *documentsv1.SchedulesPdfRequest) (*documentsv1.SchedulesPdfResponse, error) {
	f, err := s.documents.SchedulesPreview(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), req.GetMode(), req.GetTargetId())
	file, err := s.reply("SchedulesPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.SchedulesPdfResponse{File: file}, nil
}

func (s *documentsServer) RecordPdf(ctx context.Context, req *documentsv1.RecordPdfRequest) (*documentsv1.RecordPdfResponse, error) {
	f, err := s.documents.Record(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	file, err := s.reply("RecordPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.RecordPdfResponse{File: file}, nil
}

func (s *documentsServer) ReceiptPdf(ctx context.Context, req *documentsv1.ReceiptPdfRequest) (*documentsv1.ReceiptPdfResponse, error) {
	f, err := s.documents.Receipt(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	file, err := s.reply("ReceiptPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.ReceiptPdfResponse{File: file}, nil
}

// fail logs the failures nobody expected and maps every error to its status
func (s *documentsServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("documents grpc call failed")
	}
	return fromDomain(err)
}

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func signersMsg(list []domain.DocumentSigner) []*documentsv1.Signer {
	out := make([]*documentsv1.Signer, len(list))
	for i, x := range list {
		out[i] = &documentsv1.Signer{StaffId: x.StaffID, Name: x.Name, Position: x.Position}
	}
	return out
}

func versionMsg(v domain.DocumentVersion) *documentsv1.DocumentVersion {
	out := &documentsv1.DocumentVersion{Id: v.ID, Number: int32(v.Number), VerificationCode: v.VerificationCode, Status: v.Status, Note: v.Note,
		CreatedName: v.CreatedName, CreatedAt: v.CreatedAt.Format(time.RFC3339), PublishedAt: stamp(v.PublishedAt), ContentHash: v.ContentHash}
	for _, sg := range v.Signatures {
		out.Signatures = append(out.Signatures, &documentsv1.Signature{Sort: int32(sg.Sort), StaffId: sg.StaffID, Name: sg.Name, Position: sg.Position, SignedAt: stamp(sg.SignedAt), Code: sg.Code})
	}
	return out
}

// documentMsg turns a publication into its message; the scope key is period:mode:target
func (s *documentsServer) documentMsg(d domain.IssuedDocument, versions []domain.DocumentVersion, events []domain.DocumentEvent) *documentsv1.IssuedDocument {
	out := &documentsv1.IssuedDocument{Id: d.ID, Title: d.Title, TrackingCode: d.TrackingCode, TrackingUrl: s.documents.TrackingURL(d.TrackingCode, ""), InstitutionName: d.Institution}
	if parts := strings.SplitN(d.ScopeKey, ":", 3); len(parts) == 3 {
		out.Mode, out.TargetId = parts[1], parts[2]
	}
	for _, v := range versions {
		out.Versions = append(out.Versions, versionMsg(v))
	}
	for _, e := range events {
		out.Events = append(out.Events, &documentsv1.DocumentEvent{Version: int32(e.Version), Action: e.Action, Actor: e.Actor, Note: e.Note, At: e.At.Format(time.RFC3339)})
	}
	return out
}

func (s *documentsServer) SchedulesXlsx(ctx context.Context, req *documentsv1.SchedulesXlsxRequest) (*documentsv1.SchedulesXlsxResponse, error) {
	f, err := s.documents.SchedulesWorkbook(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	file, err := s.reply("SchedulesXlsx", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.SchedulesXlsxResponse{File: file}, nil
}

func (s *documentsServer) GetScheduleSigners(ctx context.Context, req *documentsv1.GetScheduleSignersRequest) (*documentsv1.GetScheduleSignersResponse, error) {
	setup, err := s.documents.Signers(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetScheduleSigners", err)
	}
	out := &documentsv1.GetScheduleSignersResponse{Signers: signersMsg(setup.Signers), CanManage: setup.CanManage}
	for _, c := range setup.Candidates {
		out.Candidates = append(out.Candidates, &documentsv1.Signer{StaffId: c.StaffID, Name: c.Name, Position: c.Position})
	}
	return out, nil
}

func (s *documentsServer) SetScheduleSigners(ctx context.Context, req *documentsv1.SetScheduleSignersRequest) (*documentsv1.SetScheduleSignersResponse, error) {
	in := make([]domain.DocumentSigner, len(req.GetSigners()))
	for i, x := range req.GetSigners() {
		in[i] = domain.DocumentSigner{StaffID: x.GetStaffId(), Position: x.GetPosition()}
	}
	list, err := s.documents.SetSigners(ctx, bearer(ctx), req.GetInstitutionSlug(), in)
	if err != nil {
		return nil, s.fail("SetScheduleSigners", err)
	}
	return &documentsv1.SetScheduleSignersResponse{Signers: signersMsg(list)}, nil
}

func (s *documentsServer) ListScheduleIssues(ctx context.Context, req *documentsv1.ListScheduleIssuesRequest) (*documentsv1.ListScheduleIssuesResponse, error) {
	issues, err := s.documents.ScheduleIssues(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("ListScheduleIssues", err)
	}
	out := &documentsv1.ListScheduleIssuesResponse{Signers: signersMsg(issues.Signers), CanManage: issues.CanManage, MyStaffId: issues.MyStaffID}
	for _, it := range issues.Items {
		out.Documents = append(out.Documents, s.documentMsg(it.Document, it.Versions, it.Events))
	}
	return out, nil
}

func (s *documentsServer) GenerateSchedules(ctx context.Context, req *documentsv1.GenerateSchedulesRequest) (*documentsv1.GenerateSchedulesResponse, error) {
	d, v, created, err := s.documents.GenerateSchedules(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), req.GetMode(), req.GetTargetId(), req.GetNote())
	if err != nil {
		return nil, s.fail("GenerateSchedules", err)
	}
	return &documentsv1.GenerateSchedulesResponse{Document: s.documentMsg(d, nil, nil), Version: versionMsg(v), Created: created}, nil
}

func (s *documentsServer) SignVersion(ctx context.Context, req *documentsv1.SignVersionRequest) (*documentsv1.SignVersionResponse, error) {
	v, published, err := s.documents.SignVersion(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetVersionId())
	if err != nil {
		return nil, s.fail("SignVersion", err)
	}
	return &documentsv1.SignVersionResponse{Version: versionMsg(v), Published: published}, nil
}

func (s *documentsServer) VersionPdf(ctx context.Context, req *documentsv1.VersionPdfRequest) (*documentsv1.VersionPdfResponse, error) {
	f, err := s.documents.VersionPdf(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetVersionId())
	file, err := s.reply("VersionPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.VersionPdfResponse{File: file}, nil
}

func (s *documentsServer) TrackDocument(ctx context.Context, req *documentsv1.TrackDocumentRequest) (*documentsv1.TrackDocumentResponse, error) {
	t, err := s.documents.Track(ctx, req.GetTrackingCode(), req.GetCode())
	if err != nil {
		return nil, s.fail("TrackDocument", err)
	}
	// The public page shows names, positions and dates; who generated a version stays inside the institution
	for i := range t.Versions {
		t.Versions[i].CreatedName = ""
		for j := range t.Versions[i].Signatures {
			t.Versions[i].Signatures[j].StaffID = ""
		}
	}
	for i := range t.Events {
		if t.Events[i].Action == "generated" || t.Events[i].Action == "cancelled" || t.Events[i].Action == "signers" {
			t.Events[i].Actor = ""
		}
	}
	d := s.documentMsg(t.Document, t.Versions, t.Events)
	d.Id, d.Mode, d.TargetId = "", "", ""
	return &documentsv1.TrackDocumentResponse{Document: d, Matched: int32(t.Matched)}, nil
}

func (s *documentsServer) TrackPdf(ctx context.Context, req *documentsv1.TrackPdfRequest) (*documentsv1.TrackPdfResponse, error) {
	f, err := s.documents.TrackPdf(ctx, req.GetTrackingCode(), int(req.GetNumber()))
	file, err := s.reply("TrackPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.TrackPdfResponse{File: file}, nil
}
