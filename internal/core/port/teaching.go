package port

import (
	"context"

	"api/internal/core/domain"
)

// StaffRepository is people.staff in PostgreSQL, scoped to institutionID like the other repositories
type StaffRepository interface {
	// StaffList returns one page and the total, with roles and assigned hours in running periods
	StaffList(ctx context.Context, f domain.StaffFilter) ([]domain.Staff, int, error)
	Staff(ctx context.Context, institutionID, id string) (domain.Staff, error)
	StaffByEmail(ctx context.Context, institutionID, email string) (domain.Staff, error)
	// CreateStaff fills an empty code with the next "P-NNNN"
	CreateStaff(ctx context.Context, institutionID string, in domain.StaffInput) (domain.Staff, error)
	UpdateStaff(ctx context.Context, institutionID, id string, in domain.StaffInput) (domain.Staff, error)
	// DeleteStaff fails with domain.ErrStaffInUse while the person has assignments or tutors a section
	DeleteStaff(ctx context.Context, institutionID, id string) error
}

// TeachingRepository is the teaching load, time blocks and timetables of the academic schema
type TeachingRepository interface {
	TimeBlocks(ctx context.Context, institutionID string) ([]domain.TimeBlock, error)
	// ReplaceTimeBlocks rewrites the day of a shift; classes in blocks that disappear are removed
	ReplaceTimeBlocks(ctx context.Context, institutionID, shift string, blocks []domain.TimeBlock) error

	Assignments(ctx context.Context, institutionID, periodID string) ([]domain.Assignment, error)
	Assignment(ctx context.Context, institutionID, id string) (domain.Assignment, error)
	StaffAssignments(ctx context.Context, institutionID, staffID string) ([]domain.Assignment, error)
	// CreateAssignments stores the missing ones (same section and subject are skipped); returns how many were added
	CreateAssignments(ctx context.Context, institutionID string, items []domain.Assignment) (int, error)
	UpdateAssignment(ctx context.Context, institutionID, id, staffID string, weeklyHours int) (domain.Assignment, error)
	DeleteAssignment(ctx context.Context, institutionID, id string) error
	SetTutor(ctx context.Context, institutionID, sectionID, staffID string) error

	// Entries returns the classes of some periods (every section)
	Entries(ctx context.Context, institutionID string, periodIDs []string) ([]domain.ScheduleEntry, error)
	// StaffEntries returns the classes of one teacher in some periods
	StaffEntries(ctx context.Context, institutionID, staffID string, periodIDs []string) ([]domain.ScheduleEntry, error)
	// SectionEntries returns the classes of one section
	SectionEntries(ctx context.Context, institutionID, sectionID string) ([]domain.ScheduleEntry, error)
	// PlaceEntry puts an assignment in a slot of its section, replacing what was there
	PlaceEntry(ctx context.Context, institutionID, periodID string, slot domain.Slot, room string) (domain.ScheduleEntry, error)
	DeleteEntry(ctx context.Context, institutionID, id string) error
	// PlaceEntries stores many slots at once (automatic timetable), skipping taken ones
	PlaceEntries(ctx context.Context, institutionID, periodID string, slots []domain.Slot) (int, error)
	ClearSection(ctx context.Context, institutionID, sectionID string) error
	// SectionsForEmail returns the sections in running periods of the student with that email, or of the students
	// whose guardian has it
	SectionsForEmail(ctx context.Context, institutionID, email string) ([]domain.PersonalSection, error)
}
