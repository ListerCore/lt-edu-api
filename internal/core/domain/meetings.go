package domain

import "time"

// Live sessions (clases en vivo): live classes of the virtual classroom, tutoring sessions and psychology
// appointments. The host picks where the call happens: inside the platform (LiveKit) or an external link (Meet)
const (
	ProviderPlatform = "platform"
	ProviderMeet     = "meet"

	SourceClass      = "class"
	SourceTutoring   = "tutoring"
	SourcePsychology = "psychology"
)

// ValidProvider reports whether v is a known provider
func ValidProvider(v string) bool { return v == ProviderPlatform || v == ProviderMeet }

// LiveTarget is what the meeting service needs to let someone into a call: the session, the person's role in it and
// who they are (StudentID is set for the student taking part)
type LiveTarget struct {
	InstitutionID string
	Source        string
	ID            string
	Title         string
	Context       string
	StartsAt      time.Time
	DurationMin   int
	Provider      string
	Link          string
	HostName      string
	IsHost        bool
	Cancelled     bool
	StudentID     string
	// Live classes: the room of the item
	RoomID string
}

// Ends is when the session is planned to end
func (t LiveTarget) Ends() time.Time {
	return t.StartsAt.Add(time.Duration(t.DurationMin) * time.Minute)
}

// Live join window for participants: from JoinEarly before the start until JoinLate after the end
const (
	JoinEarly = 15 * time.Minute
	JoinLate  = 30 * time.Minute
)

// CanJoinAt reports whether the person may enter at now: hosts any time, the rest inside the window
func (t LiveTarget) CanJoinAt(now time.Time) bool {
	if t.Cancelled {
		return false
	}
	if t.IsHost {
		return true
	}
	return !now.Before(t.StartsAt.Add(-JoinEarly)) && !now.After(t.Ends().Add(JoinLate))
}

// LiveJoin is one person who entered a call
type LiveJoin struct {
	UserID      string
	StudentID   string
	DisplayName string
	FirstAt     time.Time
	LastAt      time.Time
	Joins       int
}

// PsychAppointment is a scheduled psychology session with the student of a case
type PsychAppointment struct {
	ID          string
	CaseID      string
	StudentID   string
	StudentName string
	ScheduledAt time.Time
	DurationMin int
	Mode        string
	Provider    string
	Link        string
	Status      string
	HostUserID  string
	CreatedName string
}

var (
	ErrLiveNotConfigured = newError(KindUnavailable, "live_not_configured", "Las videollamadas dentro de la plataforma no están disponibles por ahora. Usa un enlace de Google Meet.")
	ErrLiveForbidden     = newError(KindForbidden, "live_forbidden", "No participas en esta sesión.")
	ErrLiveNotOpen       = newError(KindLocked, "live_not_open", "La sesión se abre 15 minutos antes de empezar y cierra 30 minutos después de terminar.")
	ErrLiveCancelled     = newError(KindLocked, "live_cancelled", "Esta sesión fue cancelada.")
	ErrLiveNotVirtual    = newError(KindLocked, "live_not_virtual", "Esta sesión no es virtual.")
	ErrLiveNoLink        = newError(KindLocked, "live_no_link", "El anfitrión aún no agregó el enlace de la videollamada.")
)
