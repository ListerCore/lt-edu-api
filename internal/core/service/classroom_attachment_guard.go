package service

import (
	"context"
	"errors"

	"api/internal/core/domain"
)

// Checks of the classroom on its files, beyond the role (attachment owners classroom_item and submission):
//   - material: a teacher or tutor uploads or deletes only in a room they teach; a student or guardian reads only the
//     material of their grade's room once its unit is open
//   - hand-ins: a teacher or tutor reads them only in a room they teach; the student changes the files only while the
//     attempt is still in progress (after handing in, the grade and the late flag describe what was handed in)

var errNotYourRoom = errors.New("attachment guard: not allowed")

// teachesItemRoom reports whether the account (by email) teaches the room of the item
func (s *ClassroomService) teachesItemRoom(ctx context.Context, institutionID, email, roomID string) bool {
	if email == "" {
		return false
	}
	st, err := s.repo.StaffByEmail(ctx, institutionID, email)
	if err != nil {
		return false
	}
	ok, err := s.repo.TeachesRoom(ctx, institutionID, roomID, st.ID)
	return err == nil && ok
}

// familySeesItem: the item's room is of the grade of one of the account's students in its period, and its unit is open
func (s *ClassroomService) familySeesItem(ctx context.Context, institutionID, email string, it domain.Item) bool {
	room, err := s.repo.Room(ctx, institutionID, it.RoomID)
	if err != nil {
		return false
	}
	grades, err := s.repo.StudentGrades(ctx, institutionID, room.PeriodID, email)
	if err != nil {
		return false
	}
	mine := false
	for _, g := range grades {
		if g == room.GradeID {
			mine = true
		}
	}
	if !mine {
		return false
	}
	u, err := s.repo.Unit(ctx, institutionID, it.UnitID)
	return err == nil && u.OpenOn(s.now())
}

// ItemAttachmentGuard checks the files of a piece of material
func (s *ClassroomService) ItemAttachmentGuard(ctx context.Context, a AttachmentAccess) error {
	it, err := s.repo.Item(ctx, a.InstitutionID, a.OwnerID)
	if err != nil {
		return err
	}
	switch a.Role {
	case "teacher", "tutor":
		if a.Write && !s.teachesItemRoom(ctx, a.InstitutionID, a.Email, it.RoomID) {
			return errNotYourRoom
		}
	case "student", "guardian":
		if a.Write || !s.familySeesItem(ctx, a.InstitutionID, a.Email, it) {
			return errNotYourRoom
		}
	}
	return nil
}

// SubmissionAttachmentGuard checks the files of a hand-in
func (s *ClassroomService) SubmissionAttachmentGuard(ctx context.Context, a AttachmentAccess) error {
	sub, err := s.repo.QuizSubmission(ctx, a.InstitutionID, a.OwnerID)
	if err != nil {
		return err
	}
	switch a.Role {
	case "teacher", "tutor":
		it, err := s.repo.Item(ctx, a.InstitutionID, sub.ItemID)
		if err != nil {
			return err
		}
		if !s.teachesItemRoom(ctx, a.InstitutionID, a.Email, it.RoomID) {
			return errNotYourRoom
		}
	case "student":
		if a.Write && sub.Status != domain.SubmissionInProgress {
			return errNotYourRoom
		}
	}
	return nil
}
