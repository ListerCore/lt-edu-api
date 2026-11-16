package domain

import "time"

// Virtual classroom (classroom.*): rooms, units, items, the library and the itinerant deliveries
const (
	RoomDraft     = "draft"
	RoomPublished = "published"

	ItemReading  = "reading"
	ItemFile     = "file"
	ItemVideo    = "video"
	ItemLink     = "link"
	ItemLive     = "live"
	ItemActivity = "activity"

	OwnerClassroomItem = "classroom_item"
	OwnerLibraryItem   = "library_item"
)

// Room is the virtual classroom of one subject (área) for one grade in a period
type Room struct {
	ID            string
	InstitutionID string
	PeriodID      string
	PeriodName    string
	SubjectID     string
	SubjectName   string
	SubjectCode   string
	LevelID       string
	LevelName     string
	GradeID       string
	GradeName     string
	GradeShort    string
	Description   string
	StaffID       string
	StaffName     string
	Status        string
	// Read-only counts for the catalog
	Units     int
	Items     int
	Students  int
	UpdatedAt time.Time
	// Position of level, grade and subject, for ordering
	LevelPosition, GradePosition, SubjectPosition int
}

// RoomFilter narrows the catalog. StaffID and GradeIDs restrict it to a teacher's or a student's rooms
type RoomFilter struct {
	PeriodID      string
	StaffID       string
	GradeIDs      []string
	PublishedOnly bool
}

// Unit is a block of material of a room (unidad or semana), opened to students from OpensOn when published
type Unit struct {
	ID        string
	RoomID    string
	Position  int
	Title     string
	Summary   string
	OpensOn   *time.Time
	Published bool
	Items     []Item
}

// OpenOn reports whether students see the unit on day
func (u Unit) OpenOn(day time.Time) bool {
	return u.Published && (u.OpensOn == nil || !u.OpensOn.After(day))
}

// Item is one piece of material of a unit. When LibraryItemID is set, Body, URL and the files come from the library
type Item struct {
	ID              string
	RoomID          string
	UnitID          string
	Position        int
	Kind            string
	Title           string
	Body            string
	URL             string
	Complementary   bool
	StartsAt        *time.Time
	DurationMinutes *int
	RecordingURL    string
	LibraryItemID   string
	Published       bool
	// Read-only: files (own or of the library entry) and, for a student, whether they opened it
	Files  int
	Viewed bool
	// Read-only, staff only: how many students opened it
	Views int
}

// LibraryItem is a reusable resource of the institution
type LibraryItem struct {
	ID            string
	InstitutionID string
	Kind          string
	Title         string
	Description   string
	Body          string
	URL           string
	Topic         string
	CreatedName   string
	UpdatedAt     time.Time
	// Read-only: files attached and how many items use it
	Files int
	Uses  int
}

// Delivery is the printed package of a unit handed to a student (itinerant distance form)
type Delivery struct {
	UnitID         string
	StudentID      string
	StudentName    string
	StudentCode    string
	DeliveredOn    *time.Time
	ReturnedOn     *time.Time
	Notes          string
	RecordedByName string
}

var (
	ErrClassroomForbidden = newError(KindForbidden, "classroom_forbidden", "Tu rol no puede ver el aula virtual.")
	ErrClassroomReadOnly  = newError(KindForbidden, "classroom_read_only", "Solo el docente del aula, dirección y coordinación académica pueden cambiar este material.")
	ErrRoomNotYours       = newError(KindForbidden, "room_not_yours", "Esta aula no es de tu grado o de tus cursos.")
	ErrRoomHasMaterial    = newError(KindConflict, "room_has_material", "El aula ya tiene unidades. Copia solo en un aula vacía.")
	ErrItemHasFiles       = newError(KindConflict, "item_has_files", "El material tiene archivos adjuntos. Elimínalos primero.")
	ErrUnitHasItems       = newError(KindConflict, "unit_has_items", "La unidad tiene material. Elimínalo o muévelo primero.")
	ErrLibraryInUse       = newError(KindConflict, "library_item_in_use", "Este recurso se usa en alguna aula o tiene archivos. Quítalo de las aulas y elimina sus archivos primero.")
)
