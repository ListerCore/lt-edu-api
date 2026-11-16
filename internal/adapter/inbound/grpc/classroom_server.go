package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/classroomv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// classroomServer exposes service.ClassroomService (the virtual classroom) to joseph-app's server
type classroomServer struct {
	classroomv1.UnimplementedClassroomServiceServer
	classroom *service.ClassroomService
	logger    zerolog.Logger
}

func (s *classroomServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("classroom grpc call failed")
	}
	return fromDomain(err)
}

func toRoom(r domain.Room) *classroomv1.Room {
	return &classroomv1.Room{Id: r.ID, PeriodId: r.PeriodID, PeriodName: r.PeriodName, SubjectId: r.SubjectID, SubjectName: r.SubjectName, SubjectCode: r.SubjectCode,
		LevelId: r.LevelID, LevelName: r.LevelName, GradeId: r.GradeID, GradeName: r.GradeName, GradeShort: r.GradeShort, Description: r.Description,
		StaffId: r.StaffID, StaffName: r.StaffName, Status: r.Status, Units: int32(r.Units), Items: int32(r.Items), Students: int32(r.Students),
		UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339)}
}

func toClassroomItem(it domain.Item) *classroomv1.Item {
	out := &classroomv1.Item{Id: it.ID, UnitId: it.UnitID, Kind: it.Kind, Title: it.Title, Body: it.Body, Url: it.URL, Complementary: it.Complementary,
		DurationMinutes: int32Ptr(it.DurationMinutes), RecordingUrl: it.RecordingURL, LibraryItemId: it.LibraryItemID, Published: it.Published,
		Files: int32(it.Files), Viewed: it.Viewed, Views: int32(it.Views)}
	if it.StartsAt != nil {
		out.StartsAt = it.StartsAt.UTC().Format(time.RFC3339)
	}
	return out
}

func toUnit(u domain.Unit) *classroomv1.Unit {
	out := &classroomv1.Unit{Id: u.ID, Title: u.Title, Summary: u.Summary, OpensOn: datePtr(u.OpensOn), Published: u.Published}
	for _, it := range u.Items {
		out.Items = append(out.Items, toClassroomItem(it))
	}
	return out
}

func toLibraryItem(l domain.LibraryItem) *classroomv1.LibraryItem {
	return &classroomv1.LibraryItem{Id: l.ID, Kind: l.Kind, Title: l.Title, Description: l.Description, Body: l.Body, Url: l.URL, Topic: l.Topic,
		CreatedName: l.CreatedName, UpdatedAt: l.UpdatedAt.UTC().Format(time.RFC3339), Files: int32(l.Files), Uses: int32(l.Uses)}
}

func (s *classroomServer) ListRooms(ctx context.Context, req *classroomv1.ListRoomsRequest) (*classroomv1.ListRoomsResponse, error) {
	c, err := s.classroom.Catalog(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("ListRooms", err)
	}
	out := &classroomv1.ListRoomsResponse{PeriodId: c.PeriodID, CanManage: c.CanManage, Mode: c.Mode}
	for _, p := range c.Periods {
		out.Periods = append(out.Periods, &classroomv1.Period{Id: p.ID, Name: p.Name, Status: p.Status})
	}
	for _, r := range c.Rooms {
		out.Rooms = append(out.Rooms, toRoom(r))
	}
	return out, nil
}

func (s *classroomServer) GenerateRooms(ctx context.Context, req *classroomv1.GenerateRoomsRequest) (*classroomv1.GenerateRoomsResponse, error) {
	n, err := s.classroom.GenerateRooms(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("GenerateRooms", err)
	}
	return &classroomv1.GenerateRoomsResponse{Created: int32(n)}, nil
}

func (s *classroomServer) GetRoom(ctx context.Context, req *classroomv1.GetRoomRequest) (*classroomv1.GetRoomResponse, error) {
	d, err := s.classroom.Room(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetRoom", err)
	}
	out := &classroomv1.GetRoomResponse{Room: toRoom(d.Room), CanEdit: d.CanEdit, Family: d.Family}
	for _, u := range d.Units {
		out.Units = append(out.Units, toUnit(u))
	}
	return out, nil
}

func (s *classroomServer) UpdateRoom(ctx context.Context, req *classroomv1.UpdateRoomRequest) (*classroomv1.UpdateRoomResponse, error) {
	r, err := s.classroom.UpdateRoom(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Room{ID: req.GetId(), Description: req.GetDescription(), Status: req.GetStatus(), StaffID: req.GetStaffId()})
	if err != nil {
		return nil, s.fail("UpdateRoom", err)
	}
	return &classroomv1.UpdateRoomResponse{Room: toRoom(r)}, nil
}

func (s *classroomServer) CopyRoom(ctx context.Context, req *classroomv1.CopyRoomRequest) (*classroomv1.CopyRoomResponse, error) {
	n, err := s.classroom.CopyRoom(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetFromRoomId())
	if err != nil {
		return nil, s.fail("CopyRoom", err)
	}
	return &classroomv1.CopyRoomResponse{Units: int32(n)}, nil
}

func (s *classroomServer) SaveUnit(ctx context.Context, req *classroomv1.SaveUnitRequest) (*classroomv1.SaveUnitResponse, error) {
	opens, err := optionalDate("opensOn", req.GetOpensOn())
	if err != nil {
		return nil, s.fail("SaveUnit", err)
	}
	u, err := s.classroom.SaveUnit(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Unit{ID: req.GetId(), RoomID: req.GetRoomId(), Title: req.GetTitle(),
		Summary: req.GetSummary(), OpensOn: opens, Published: req.GetPublished()})
	if err != nil {
		return nil, s.fail("SaveUnit", err)
	}
	return &classroomv1.SaveUnitResponse{Unit: toUnit(u)}, nil
}

func (s *classroomServer) DeleteUnit(ctx context.Context, req *classroomv1.DeleteUnitRequest) (*classroomv1.DeleteUnitResponse, error) {
	if err := s.classroom.DeleteUnit(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteUnit", err)
	}
	return &classroomv1.DeleteUnitResponse{}, nil
}

func (s *classroomServer) ReorderUnits(ctx context.Context, req *classroomv1.ReorderUnitsRequest) (*classroomv1.ReorderUnitsResponse, error) {
	if err := s.classroom.ReorderUnits(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetRoomId(), req.GetIds()); err != nil {
		return nil, s.fail("ReorderUnits", err)
	}
	return &classroomv1.ReorderUnitsResponse{}, nil
}

func (s *classroomServer) SaveItem(ctx context.Context, req *classroomv1.SaveItemRequest) (*classroomv1.SaveItemResponse, error) {
	in := req.GetItem()
	it := domain.Item{ID: in.GetId(), UnitID: in.GetUnitId(), Kind: in.GetKind(), Title: in.GetTitle(), Body: in.GetBody(), URL: in.GetUrl(),
		Complementary: in.GetComplementary(), DurationMinutes: intPtr(in.DurationMinutes), RecordingURL: in.GetRecordingUrl(),
		LibraryItemID: in.GetLibraryItemId(), Published: in.GetPublished()}
	if in.GetStartsAt() != "" {
		at, err := parseInstant("startsAt", in.GetStartsAt())
		if err != nil {
			return nil, s.fail("SaveItem", err)
		}
		it.StartsAt = &at
	}
	out, err := s.classroom.SaveItem(ctx, bearer(ctx), req.GetInstitutionSlug(), it)
	if err != nil {
		return nil, s.fail("SaveItem", err)
	}
	return &classroomv1.SaveItemResponse{Item: toClassroomItem(out)}, nil
}

func (s *classroomServer) DeleteItem(ctx context.Context, req *classroomv1.DeleteItemRequest) (*classroomv1.DeleteItemResponse, error) {
	if err := s.classroom.DeleteItem(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteItem", err)
	}
	return &classroomv1.DeleteItemResponse{}, nil
}

func (s *classroomServer) ReorderItems(ctx context.Context, req *classroomv1.ReorderItemsRequest) (*classroomv1.ReorderItemsResponse, error) {
	if err := s.classroom.ReorderItems(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetUnitId(), req.GetIds()); err != nil {
		return nil, s.fail("ReorderItems", err)
	}
	return &classroomv1.ReorderItemsResponse{}, nil
}

func (s *classroomServer) RecordView(ctx context.Context, req *classroomv1.RecordViewRequest) (*classroomv1.RecordViewResponse, error) {
	if err := s.classroom.RecordView(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetItemId()); err != nil {
		return nil, s.fail("RecordView", err)
	}
	return &classroomv1.RecordViewResponse{}, nil
}

func (s *classroomServer) ListLibrary(ctx context.Context, req *classroomv1.ListLibraryRequest) (*classroomv1.ListLibraryResponse, error) {
	v, err := s.classroom.Library(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListLibrary", err)
	}
	out := &classroomv1.ListLibraryResponse{CanEdit: v.CanEdit}
	for _, l := range v.Items {
		out.Items = append(out.Items, toLibraryItem(l))
	}
	return out, nil
}

func (s *classroomServer) SaveLibraryItem(ctx context.Context, req *classroomv1.SaveLibraryItemRequest) (*classroomv1.SaveLibraryItemResponse, error) {
	in := req.GetItem()
	l, err := s.classroom.SaveLibraryItem(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.LibraryItem{ID: in.GetId(), Kind: in.GetKind(), Title: in.GetTitle(),
		Description: in.GetDescription(), Body: in.GetBody(), URL: in.GetUrl(), Topic: in.GetTopic()})
	if err != nil {
		return nil, s.fail("SaveLibraryItem", err)
	}
	return &classroomv1.SaveLibraryItemResponse{Item: toLibraryItem(l)}, nil
}

func (s *classroomServer) DeleteLibraryItem(ctx context.Context, req *classroomv1.DeleteLibraryItemRequest) (*classroomv1.DeleteLibraryItemResponse, error) {
	if err := s.classroom.DeleteLibraryItem(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteLibraryItem", err)
	}
	return &classroomv1.DeleteLibraryItemResponse{}, nil
}

func (s *classroomServer) ListDeliveries(ctx context.Context, req *classroomv1.ListDeliveriesRequest) (*classroomv1.ListDeliveriesResponse, error) {
	list, err := s.classroom.Deliveries(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetUnitId())
	if err != nil {
		return nil, s.fail("ListDeliveries", err)
	}
	out := &classroomv1.ListDeliveriesResponse{}
	for _, d := range list {
		out.Deliveries = append(out.Deliveries, &classroomv1.Delivery{UnitId: d.UnitID, StudentId: d.StudentID, StudentName: d.StudentName, StudentCode: d.StudentCode,
			DeliveredOn: datePtr(d.DeliveredOn), ReturnedOn: datePtr(d.ReturnedOn), Notes: d.Notes, RecordedByName: d.RecordedByName})
	}
	return out, nil
}

func (s *classroomServer) SaveDelivery(ctx context.Context, req *classroomv1.SaveDeliveryRequest) (*classroomv1.SaveDeliveryResponse, error) {
	in := req.GetDelivery()
	delivered, err := optionalDate("deliveredOn", in.GetDeliveredOn())
	if err != nil {
		return nil, s.fail("SaveDelivery", err)
	}
	returned, err := optionalDate("returnedOn", in.GetReturnedOn())
	if err != nil {
		return nil, s.fail("SaveDelivery", err)
	}
	if err := s.classroom.SaveDelivery(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Delivery{UnitID: in.GetUnitId(), StudentID: in.GetStudentId(),
		DeliveredOn: delivered, ReturnedOn: returned, Notes: in.GetNotes()}); err != nil {
		return nil, s.fail("SaveDelivery", err)
	}
	return &classroomv1.SaveDeliveryResponse{}, nil
}
