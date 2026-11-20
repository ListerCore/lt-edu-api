package grpc

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/compliancev1"
	"api/internal/adapter/inbound/grpc/gen/documentsv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// complianceServer exposes service.ComplianceService (phase E8) to lt-edu-app's server
type complianceServer struct {
	compliancev1.UnimplementedComplianceServiceServer
	compliance *service.ComplianceService
	logger     zerolog.Logger
}

func (s *complianceServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("compliance grpc call failed")
	}
	return fromDomain(err)
}

func toEquipmentRoom(r domain.EquipmentRoom) *compliancev1.Room {
	return &compliancev1.Room{Id: r.ID, Name: r.Name, Location: r.Location, AreaM2: r.AreaM2, Workstations: int32(r.Workstations), Furniture: r.Furniture,
		PowerSupply: r.PowerSupply, Notes: r.Notes, Devices: int32(r.Devices), Files: int32(r.Files)}
}

func toEquipmentDevice(d domain.Device) *compliancev1.Device {
	return &compliancev1.Device{Id: d.ID, Code: d.Code, Kind: d.Kind, Brand: d.Brand, Model: d.Model, Specs: d.Specs, RoomId: d.RoomID, RoomName: d.RoomName,
		Purpose: d.Purpose, StaffId: d.StaffID, StaffName: d.StaffName, AssignedTo: d.AssignedTo, Status: d.Status, Notes: d.Notes, Files: int32(d.Files), LastAccess: instant(d.LastAccess)}
}

func optInt32(v *int) *int32 {
	if v == nil {
		return nil
	}
	x := int32(*v)
	return &x
}

func optInt(v *int32) *int {
	if v == nil {
		return nil
	}
	x := int(*v)
	return &x
}

func toEquipmentCheck(c domain.EquipmentCheck) *compliancev1.Check {
	return &compliancev1.Check{Id: c.ID, Kind: c.Kind, RoomId: c.RoomID, RoomName: c.RoomName, DeviceId: c.DeviceID, DeviceCode: c.DeviceCode, CheckedOn: formatDate(c.CheckedOn),
		Provider: c.Provider, LinkKind: c.LinkKind, DownloadMbps: c.DownloadMbps, UploadMbps: c.UploadMbps, LatencyMs: optInt32(c.LatencyMs), Profile: c.Profile,
		Passed: c.Passed, Result: c.Result, CheckedByName: c.CheckedByName, Files: int32(c.Files)}
}

func toComplianceReport(r domain.ComplianceReport) *compliancev1.Report {
	return &compliancev1.Report{Kind: r.Kind, Number: r.Number, FileNumber: r.FileNumber, City: r.City, Location: r.Location, EvaluatedOn: datePtr(r.EvaluatedOn),
		GeneralResult: r.GeneralResult, Limitations: r.Limitations, Conclusion: r.Conclusion, UpdatedByName: r.UpdatedByName, UpdatedAt: instant(r.UpdatedAt)}
}

func toComplianceItem(it domain.ComplianceItem) *compliancev1.Item {
	return &compliancev1.Item{Id: it.ID, Report: it.Report, Section: it.Section, Code: it.Code, Position: int32(it.Position), Element: it.Element, Module: it.Module,
		Route: it.Route, Annex: it.Annex, Folio: it.Folio, Status: it.Status, Notes: it.Notes, VerifiedByName: it.VerifiedByName, VerifiedAt: instant(it.VerifiedAt), Files: int32(it.Files)}
}

func toUptimeCheck(c domain.UptimeCheck) *compliancev1.UptimeCheck {
	return &compliancev1.UptimeCheck{Url: c.URL, CheckedAt: instant(&c.CheckedAt), StatusCode: optInt32(c.StatusCode), ResponseMs: optInt32(c.ResponseMs), Ok: c.OK, Error: c.Error}
}

func (s *complianceServer) GetBoard(ctx context.Context, req *compliancev1.GetBoardRequest) (*compliancev1.GetBoardResponse, error) {
	b, err := s.compliance.Board(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetBoard", err)
	}
	out := &compliancev1.GetBoardResponse{CanWrite: b.CanWrite, CanVerify: b.CanVerify, Platform: &compliancev1.PlatformInfo{PlatformName: b.Platform.PlatformName, Url: b.Platform.URL,
		TechnicalLead: b.Platform.TechnicalLead, Provider: b.Platform.Provider, Instance: b.Platform.Instance, Files: int32(b.Platform.Files)},
		Uptime: &compliancev1.Uptime{Url: b.Uptime.URL, Checks: int32(b.Uptime.Checks), Up: int32(b.Uptime.Up), Percent: b.Uptime.Percent, AvgMs: int32(b.Uptime.AvgMs)}}
	if b.Uptime.Last != nil {
		out.Uptime.Last = toUptimeCheck(*b.Uptime.Last)
	}
	for _, c := range b.Uptime.Recent {
		out.Uptime.Recent = append(out.Uptime.Recent, toUptimeCheck(c))
	}
	for _, r := range b.Reports {
		out.Reports = append(out.Reports, toComplianceReport(r))
	}
	for _, it := range b.Items {
		out.Items = append(out.Items, toComplianceItem(it))
	}
	for _, r := range b.Rooms {
		out.Rooms = append(out.Rooms, toEquipmentRoom(r))
	}
	for _, d := range b.Devices {
		out.Devices = append(out.Devices, toEquipmentDevice(d))
	}
	for _, c := range b.Checks {
		out.Checks = append(out.Checks, toEquipmentCheck(c))
	}
	return out, nil
}

func (s *complianceServer) SaveRoom(ctx context.Context, req *compliancev1.SaveRoomRequest) (*compliancev1.SaveRoomResponse, error) {
	r := req.GetRoom()
	out, err := s.compliance.SaveRoom(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.EquipmentRoom{ID: r.GetId(), Name: r.GetName(), Location: r.GetLocation(), AreaM2: r.AreaM2,
		Workstations: int(r.GetWorkstations()), Furniture: r.GetFurniture(), PowerSupply: r.GetPowerSupply(), Notes: r.GetNotes()})
	if err != nil {
		return nil, s.fail("SaveRoom", err)
	}
	return &compliancev1.SaveRoomResponse{Room: toEquipmentRoom(out)}, nil
}

func (s *complianceServer) DeleteRoom(ctx context.Context, req *compliancev1.DeleteRoomRequest) (*compliancev1.DeleteRoomResponse, error) {
	if err := s.compliance.DeleteRoom(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteRoom", err)
	}
	return &compliancev1.DeleteRoomResponse{}, nil
}

func (s *complianceServer) SaveDevice(ctx context.Context, req *compliancev1.SaveDeviceRequest) (*compliancev1.SaveDeviceResponse, error) {
	d := req.GetDevice()
	out, err := s.compliance.SaveDevice(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Device{ID: d.GetId(), Code: d.GetCode(), Kind: d.GetKind(), Brand: d.GetBrand(),
		Model: d.GetModel(), Specs: d.GetSpecs(), RoomID: d.GetRoomId(), Purpose: d.GetPurpose(), StaffID: d.GetStaffId(), AssignedTo: d.GetAssignedTo(), Status: d.GetStatus(), Notes: d.GetNotes()})
	if err != nil {
		return nil, s.fail("SaveDevice", err)
	}
	return &compliancev1.SaveDeviceResponse{Device: toEquipmentDevice(out)}, nil
}

func (s *complianceServer) DeleteDevice(ctx context.Context, req *compliancev1.DeleteDeviceRequest) (*compliancev1.DeleteDeviceResponse, error) {
	if err := s.compliance.DeleteDevice(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteDevice", err)
	}
	return &compliancev1.DeleteDeviceResponse{}, nil
}

func (s *complianceServer) AddCheck(ctx context.Context, req *compliancev1.AddCheckRequest) (*compliancev1.AddCheckResponse, error) {
	c := req.GetCheck()
	on, err := requiredDate("checkedOn", c.GetCheckedOn())
	if err != nil {
		return nil, s.fail("AddCheck", err)
	}
	out, err := s.compliance.AddCheck(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.EquipmentCheck{Kind: c.GetKind(), RoomID: c.GetRoomId(), DeviceID: c.GetDeviceId(), CheckedOn: on,
		Provider: c.GetProvider(), LinkKind: c.GetLinkKind(), DownloadMbps: c.DownloadMbps, UploadMbps: c.UploadMbps, LatencyMs: optInt(c.LatencyMs), Profile: c.GetProfile(),
		Passed: c.GetPassed(), Result: c.GetResult()})
	if err != nil {
		return nil, s.fail("AddCheck", err)
	}
	return &compliancev1.AddCheckResponse{Check: toEquipmentCheck(out)}, nil
}

func (s *complianceServer) DeleteCheck(ctx context.Context, req *compliancev1.DeleteCheckRequest) (*compliancev1.DeleteCheckResponse, error) {
	if err := s.compliance.DeleteCheck(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteCheck", err)
	}
	return &compliancev1.DeleteCheckResponse{}, nil
}

func (s *complianceServer) SaveItem(ctx context.Context, req *compliancev1.SaveItemRequest) (*compliancev1.SaveItemResponse, error) {
	it, err := s.compliance.SaveItem(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.ComplianceItem{ID: req.GetId(), Module: req.GetModule(), Route: req.GetRoute(),
		Annex: req.GetAnnex(), Folio: req.GetFolio(), Notes: req.GetNotes()})
	if err != nil {
		return nil, s.fail("SaveItem", err)
	}
	return &compliancev1.SaveItemResponse{Item: toComplianceItem(it)}, nil
}

func (s *complianceServer) SetItemStatus(ctx context.Context, req *compliancev1.SetItemStatusRequest) (*compliancev1.SetItemStatusResponse, error) {
	it, err := s.compliance.SetItemStatus(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus())
	if err != nil {
		return nil, s.fail("SetItemStatus", err)
	}
	return &compliancev1.SetItemStatusResponse{Item: toComplianceItem(it)}, nil
}

func (s *complianceServer) SaveReport(ctx context.Context, req *compliancev1.SaveReportRequest) (*compliancev1.SaveReportResponse, error) {
	r := req.GetReport()
	on, err := optionalDate("evaluatedOn", r.GetEvaluatedOn())
	if err != nil {
		return nil, s.fail("SaveReport", err)
	}
	out, err := s.compliance.SaveReport(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.ComplianceReport{Kind: r.GetKind(), Number: r.GetNumber(), FileNumber: r.GetFileNumber(),
		City: r.GetCity(), Location: r.GetLocation(), EvaluatedOn: on, GeneralResult: r.GetGeneralResult(), Limitations: r.GetLimitations(), Conclusion: r.GetConclusion()})
	if err != nil {
		return nil, s.fail("SaveReport", err)
	}
	return &compliancev1.SaveReportResponse{Report: toComplianceReport(out)}, nil
}

func (s *complianceServer) CheckUptime(ctx context.Context, req *compliancev1.CheckUptimeRequest) (*compliancev1.CheckUptimeResponse, error) {
	c, err := s.compliance.CheckNow(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("CheckUptime", err)
	}
	return &compliancev1.CheckUptimeResponse{Check: toUptimeCheck(c)}, nil
}

func (s *complianceServer) pdf(method string, f service.PdfFile, err error) (*documentsv1.PdfFile, error) {
	if err != nil {
		return nil, s.fail(method, err)
	}
	return &documentsv1.PdfFile{FileName: f.Name, Content: f.Content}, nil
}

func (s *complianceServer) PlatformReportPdf(ctx context.Context, req *compliancev1.PlatformReportPdfRequest) (*compliancev1.PlatformReportPdfResponse, error) {
	f, err := s.compliance.PlatformReportPdf(ctx, bearer(ctx), req.GetInstitutionSlug())
	file, err := s.pdf("PlatformReportPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &compliancev1.PlatformReportPdfResponse{File: file}, nil
}

func (s *complianceServer) EquipmentReportPdf(ctx context.Context, req *compliancev1.EquipmentReportPdfRequest) (*compliancev1.EquipmentReportPdfResponse, error) {
	f, err := s.compliance.EquipmentReportPdf(ctx, bearer(ctx), req.GetInstitutionSlug())
	file, err := s.pdf("EquipmentReportPdf", f, err)
	if err != nil {
		return nil, err
	}
	return &compliancev1.EquipmentReportPdfResponse{File: file}, nil
}
