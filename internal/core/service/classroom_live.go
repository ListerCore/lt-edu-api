package service

import (
	"context"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Live classes of the virtual classroom, for the meeting service (clases en vivo)

// SetMeetings gives the classroom the live sessions' store (agenda of live classes)
func (s *ClassroomService) SetMeetings(r port.MeetingsRepository) { s.meetings = r }

func roomContext(r domain.Room) string {
	if r.GradeName == "" {
		return r.SubjectName
	}
	return r.SubjectName + " · " + r.GradeName
}

// LiveTarget lets the meeting service into a live class: the room's teacher, dirección and coordinación host it;
// the students of the grade join a published item of an open unit (and their entry counts as a view, which feeds
// participation). Guardians only read the room; readers (secretaría, responsable técnico) may enter to support
func (s *ClassroomService) LiveTarget(ctx context.Context, token, slug, itemID string) (domain.LiveTarget, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.LiveTarget{}, err
	}
	it, err := s.repo.Item(ctx, sc.institution.ID, itemID)
	if err != nil {
		return domain.LiveTarget{}, err
	}
	if it.Kind != domain.ItemLive || it.StartsAt == nil || it.DurationMinutes == nil {
		return domain.LiveTarget{}, domain.ErrNotFound
	}
	room, canEdit, viewer, err := s.roomAccess(ctx, sc, it.RoomID)
	if err != nil {
		return domain.LiveTarget{}, err
	}
	if sc.family() {
		if sc.role != "student" {
			return domain.LiveTarget{}, domain.ErrLiveForbidden
		}
		u, err := s.repo.Unit(ctx, sc.institution.ID, it.UnitID)
		if err != nil {
			return domain.LiveTarget{}, err
		}
		if viewer == "" || !it.Published || !u.OpenOn(s.now()) {
			return domain.LiveTarget{}, domain.ErrNotFound
		}
	}
	return domain.LiveTarget{InstitutionID: sc.institution.ID, Source: domain.SourceClass, ID: it.ID, RoomID: it.RoomID, Title: it.Title,
		Context: roomContext(room), StartsAt: *it.StartsAt, DurationMin: *it.DurationMinutes, Provider: it.Provider, Link: it.URL,
		HostName: room.StaffName, IsHost: canEdit, StudentID: viewer}, nil
}

// RecordLiveView counts the student's entry into a live class as opening the item
func (s *ClassroomService) RecordLiveView(ctx context.Context, itemID, studentID string) error {
	if studentID == "" {
		return nil
	}
	return s.repo.RecordView(ctx, itemID, studentID, s.now())
}

// LiveAgenda lists the live classes of the person's rooms in the running period between from and to: a teacher's
// rooms (host), a student's published rooms (only what is open to them), every room for dirección and coordinación
func (s *ClassroomService) LiveAgenda(ctx context.Context, token, slug string, from, to time.Time) ([]domain.LiveTarget, error) {
	if s.meetings == nil {
		return []domain.LiveTarget{}, nil
	}
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if sc.family() && sc.role != "student" {
		return []domain.LiveTarget{}, nil
	}
	cat, err := s.Catalog(ctx, token, slug, "")
	if err != nil {
		return nil, err
	}
	if cat.Mode == "reader" {
		return []domain.LiveTarget{}, nil
	}
	rooms := map[string]domain.Room{}
	ids := make([]string, 0, len(cat.Rooms))
	for _, r := range cat.Rooms {
		rooms[r.ID] = r
		ids = append(ids, r.ID)
	}
	list, err := s.meetings.LiveItems(ctx, sc.institution.ID, ids, from, to, sc.family(), s.now().In(lima()))
	if err != nil {
		return nil, err
	}
	for i := range list {
		r := rooms[list[i].RoomID]
		list[i].Context, list[i].HostName, list[i].IsHost = roomContext(r), r.StaffName, !sc.family()
	}
	return list, nil
}
