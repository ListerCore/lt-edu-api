package domain

import "time"

// Participation and permanence of the distance form (phase E5)

// PermanenceRule is how an institution measures permanence: a window of WindowDays counts when the student was
// active on MinActiveDays of its days. InactiveDays without activity and a permanence under LowPercent raise alerts
type PermanenceRule struct {
	WindowDays    int
	MinActiveDays int
	InactiveDays  int
	LowPercent    int
	UpdatedByName string
	UpdatedAt     *time.Time
}

// DefaultPermanenceRule is one active day a week, an alert after two weeks without activity or under 60 %
var DefaultPermanenceRule = PermanenceRule{WindowDays: 7, MinActiveDays: 1, InactiveDays: 14, LowPercent: 60}

// DistanceSection is a distance section of a period with how many students it has
type DistanceSection struct {
	ID        string
	Name      string
	LevelName string
	GradeName string
	Variant   string
	TutorName string
	Students  int
}

// PermanenceStudent is an enrollment of a distance section and its dates, for the windows
type PermanenceStudent struct {
	EnrollmentID string
	StudentID    string
	StudentName  string
	StudentCode  string
	Status       string
	EnrolledOn   time.Time
	EndedOn      *time.Time
}

// PermanenceAdjustment is the tutor's decision on one window of an enrollment
type PermanenceAdjustment struct {
	EnrollmentID string
	WindowStart  time.Time
	Present      bool
	Reason       string
	ByName       string
}

// PermanenceWindow is one window of a student's permanence
type PermanenceWindow struct {
	Start      time.Time
	End        time.Time
	ActiveDays int
	Present    bool
	Adjusted   bool
	Reason     string
}

// StudentActivity is what a student did in a range: active days and totals per kind, and the last action
type StudentActivity struct {
	StudentID   string
	Days        []time.Time
	Logins      int
	Views       int
	Live        int
	Attempts    int
	Submissions int
	Posts       int
	Tutoring    int
	LastAt      *time.Time
}

// RoomParticipation is a student's participation in one classroom room
type RoomParticipation struct {
	StudentID   string
	StudentName string
	StudentCode string
	ItemsSeen   int
	Views       int
	Live        int
	Attempts    int
	Submissions int
	Posts       int
	ActiveDays  int
	LastAt      *time.Time
}

// DropoutSignal is an active student of a distance section with the facts the dropout alerts read
type DropoutSignal struct {
	EnrollmentID string
	StudentID    string
	StudentName  string
	Placement    string
	EnrolledOn   time.Time
	LastAt       *time.Time
	// Tasks and quizzes of the student's rooms past their deadline without anything handed in
	Overdue int
}

var (
	ErrParticipationForbidden = newError(KindForbidden, "participation_forbidden", "Tu rol no puede ver la participación de los estudiantes.")
	ErrPermanenceReadOnly     = newError(KindForbidden, "permanence_read_only", "Solo dirección, coordinación o el tutor de la sección pueden ajustar la permanencia.")
	ErrNotDistanceSection     = newError(KindInvalid, "not_distance_section", "La permanencia se mide en las secciones a distancia; las demás usan la asistencia por sesión.")
)
