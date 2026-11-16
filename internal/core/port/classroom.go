package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// ClassroomRepository is classroom.* in PostgreSQL. Every method that takes institutionID only touches rows of that
// institution; an id of another one behaves as missing (domain.ErrNotFound)
type ClassroomRepository interface {
	InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error)
	// StaffByEmail is the active staff record of the account (domain.ErrNotFound when none)
	StaffByEmail(ctx context.Context, institutionID, email string) (domain.Staff, error)
	// StudentGrades maps each active student of the email (own or as guardian) to their grade in the period
	StudentGrades(ctx context.Context, institutionID, periodID, email string) (map[string]string, error)
	// RunningPeriod is the id of the period in course (the newest active one), or "" when none
	RunningPeriod(ctx context.Context, institutionID string) (string, error)
	// Periods lists the institution's periods (id, name, status, dates), newest first
	Periods(ctx context.Context, institutionID string) ([]domain.Period, error)

	Rooms(ctx context.Context, institutionID string, f domain.RoomFilter) ([]domain.Room, error)
	Room(ctx context.Context, institutionID, roomID string) (domain.Room, error)
	// EnsureRooms creates the missing rooms of the period: one per subject (area, course or unit) × grade of the
	// plans in force for the levels with sections in it. Returns how many it added
	EnsureRooms(ctx context.Context, institutionID, periodID string) (int, error)
	UpdateRoom(ctx context.Context, institutionID string, r domain.Room) (domain.Room, error)
	// TeachesRoom reports whether the staff member teaches the room's subject in a section of its grade
	TeachesRoom(ctx context.Context, institutionID, roomID, staffID string) (bool, error)

	// Units returns the room's units in order, with their items; withViews counts views per item, studentID marks
	// the items that student opened
	Units(ctx context.Context, roomID string, withViews bool, studentID string) ([]domain.Unit, error)
	Unit(ctx context.Context, institutionID, unitID string) (domain.Unit, error)
	SaveUnit(ctx context.Context, institutionID string, u domain.Unit) (domain.Unit, error)
	DeleteUnit(ctx context.Context, institutionID, unitID string) error
	ReorderUnits(ctx context.Context, institutionID, roomID string, ids []string) error
	// CopyUnits copies every unit and item (without files of their own) of one room into another empty one
	CopyUnits(ctx context.Context, institutionID, fromRoomID, toRoomID string) (int, error)

	Item(ctx context.Context, institutionID, itemID string) (domain.Item, error)
	SaveItem(ctx context.Context, institutionID, userID string, it domain.Item) (domain.Item, error)
	DeleteItem(ctx context.Context, institutionID, itemID string) error
	ReorderItems(ctx context.Context, institutionID, unitID string, ids []string) error
	// RecordView counts one opening of the item by the student
	RecordView(ctx context.Context, itemID, studentID string, at time.Time) error

	Library(ctx context.Context, institutionID string) ([]domain.LibraryItem, error)
	LibraryItem(ctx context.Context, institutionID, id string) (domain.LibraryItem, error)
	SaveLibraryItem(ctx context.Context, institutionID, userID, userName string, l domain.LibraryItem) (domain.LibraryItem, error)
	DeleteLibraryItem(ctx context.Context, institutionID, id string) error

	// Deliveries lists the students of the room's grade in itinerant sections of the period with the delivery of
	// that unit, if any
	Deliveries(ctx context.Context, institutionID, unitID string) ([]domain.Delivery, error)
	SaveDelivery(ctx context.Context, institutionID string, d domain.Delivery) error
}
