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

	// ---------- online assessments (phase E3) ----------

	// EnsureAssessment creates the default settings of a new quiz or task item (one attempt, no window)
	EnsureAssessment(ctx context.Context, itemID string) error
	// Assessment reads the settings, the item and the questions (with the right answers)
	Assessment(ctx context.Context, institutionID, itemID string) (domain.Assessment, error)
	// SaveAssessment writes the settings; questions, when non-nil, replace the current ones
	SaveAssessment(ctx context.Context, institutionID string, a domain.Assessment, questions []domain.QuizQuestion) (domain.Assessment, error)
	// HandedIn counts the attempts already handed in (submitted or graded)
	HandedIn(ctx context.Context, itemID string) (int, error)
	// RoomCompetencies are the competencies of the room's area
	RoomCompetencies(ctx context.Context, institutionID, roomID string) ([]domain.CompetencyOption, error)

	// Roster lists the students of the room's grade with their latest attempt, attempt count and extension
	Roster(ctx context.Context, institutionID, itemID string) ([]domain.QuizRosterRow, error)
	// Attempts are a student's attempts at the item, newest first
	Attempts(ctx context.Context, itemID, studentID string) ([]domain.QuizSubmission, error)
	QuizSubmission(ctx context.Context, institutionID, id string) (domain.QuizSubmission, error)
	CreateAttempt(ctx context.Context, itemID, studentID string, attempt int) (domain.QuizSubmission, error)
	// SaveAnswers only touches an attempt still in progress
	SaveAnswers(ctx context.Context, id string, answers map[string]domain.QuizAnswer) error
	HandIn(ctx context.Context, id string, autoPoints, autoTotal int, late bool, at time.Time) error
	Grade(ctx context.Context, institutionID string, s domain.QuizSubmission, userID, userName string) (domain.QuizSubmission, error)
	Extension(ctx context.Context, itemID, studentID string) (*time.Time, error)
	// SetExtension saves (or, with nil, removes) one student's deadline
	SetExtension(ctx context.Context, itemID, studentID string, closesAt *time.Time, reason, grantedName string) error

	// RecordTargets are the latest graded attempts with levels, each with the student's section and the teaching
	// assignment of the room's area there (assignment "" when the area is not assigned in that section)
	RecordTargets(ctx context.Context, institutionID, itemID string) ([]domain.RecordTarget, error)
	// OpenTerm is the term of the period that contains day and is still open ("" when none)
	OpenTerm(ctx context.Context, periodID string, day time.Time) (string, error)
	MarkRecorded(ctx context.Context, ids []string, at time.Time) error

	// ---------- forums (phase E4) ----------

	// EnsureForum creates the settings of a new forum item (a debate, open)
	EnsureForum(ctx context.Context, itemID string) error
	Forum(ctx context.Context, institutionID, itemID string) (domain.Forum, error)
	SaveForum(ctx context.Context, institutionID string, f domain.Forum) error
	// Posts are every post of the forum, threads and replies, oldest first
	Posts(ctx context.Context, itemID string) ([]domain.ForumPost, error)
	Post(ctx context.Context, institutionID, id string) (domain.ForumPost, error)
	CreatePost(ctx context.Context, p domain.ForumPost) (domain.ForumPost, error)
	EditPost(ctx context.Context, id, body string, at time.Time) error
	DeletePost(ctx context.Context, id string) error
	// ModeratePost sets pinned and hidden (with reason and who) of a post
	ModeratePost(ctx context.Context, p domain.ForumPost) error
	// LastRead is when the user last opened the forum (nil: never); MarkRead sets it to at
	LastRead(ctx context.Context, itemID, userID string) (*time.Time, error)
	MarkRead(ctx context.Context, itemID, userID string, at time.Time) error
	// ForumParticipation counts the threads and replies of each student of the room's grade
	ForumParticipation(ctx context.Context, institutionID, itemID string) ([]domain.ForumParticipation, error)
}
