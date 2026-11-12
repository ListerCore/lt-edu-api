package domain

import "time"

// APAFA meeting kinds, statuses, modalities and audiences (apafa.meetings)
const (
	ApafaOrdinary      = "ordinary"
	ApafaExtraordinary = "extraordinary"
	ApafaAssembly      = "assembly"
	ApafaBoard         = "board"

	ApafaScheduled   = "scheduled"
	ApafaRescheduled = "rescheduled"
	ApafaHeld        = "held"
	ApafaCancelled   = "cancelled"

	ApafaInPerson = "in_person"
	ApafaVirtual  = "virtual"
	ApafaHybrid   = "hybrid"
)

// ApafaMeeting is one meeting of the parents' association: when and where, the agenda, its status and, once held,
// the acta summary and the agreements
type ApafaMeeting struct {
	ID               string
	InstitutionID    string
	Title            string
	Kind             string
	ScheduledAt      time.Time
	Modality         string
	Place            string
	MeetingURL       string
	Agenda           string
	Audience         string
	Status           string
	PreviousAt       *time.Time
	RescheduleReason string
	CancelReason     string
	Minutes          string
	Agreements       string
	Attendees        *int
	MinutesAt        *time.Time
	AnnouncementID   string
	NotifiedAt       *time.Time
	CreatedBy        string
	CreatedName      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// Read-only: files hung on it (attachments.files)
	Files int
}

// Open reports whether the meeting is still to be held (scheduled or moved)
func (m ApafaMeeting) Open() bool { return m.Status == ApafaScheduled || m.Status == ApafaRescheduled }

// ApafaBoardMember is one office of the association's board (consejo directivo) for a period
type ApafaBoardMember struct {
	ID            string
	InstitutionID string
	FullName      string
	Position      string
	Phone         string
	StartsOn      time.Time
	EndsOn        time.Time
}

// ApafaFees is the collection of the APAFA fee (finance concept apafa) in the running periods of the institution
type ApafaFees struct {
	Expected  Money
	Collected Money
	Pending   Money
	// Students charged the fee, and those who settled it
	Charged int
	Settled int
}

var (
	ErrApafaPrivate   = newError(KindForbidden, "apafa_private", "La APAFA solo existe en instituciones públicas de Educación Básica. Revisa el tipo de gestión en la configuración de la institución.")
	ErrApafaForbidden = newError(KindForbidden, "apafa_forbidden", "Tu rol no puede ver las reuniones de la APAFA.")
	ErrApafaReadOnly  = newError(KindForbidden, "apafa_read_only", "Solo dirección y secretaría convocan y registran las reuniones de la APAFA.")
	ErrApafaClosed    = newError(KindConflict, "apafa_meeting_closed", "La reunión ya se realizó o se canceló: no se puede cambiar.")
	ErrApafaNotHeld   = newError(KindConflict, "apafa_meeting_not_held", "Solo se registra el acta de una reunión que no fue cancelada.")
	ErrApafaHasFiles  = newError(KindConflict, "apafa_meeting_has_files", "La reunión tiene archivos adjuntos o ya se convocó: cancélala en lugar de eliminarla.")
	ErrApafaNotified  = newError(KindConflict, "apafa_meeting_notified", "Esta reunión ya fue convocada.")
	// The meeting change is stored; only the announcement to the families failed
	ErrApafaNoticeFailed = newError(KindUnavailable, "apafa_notice_failed", "Se guardó la reunión, pero no pudimos publicar el aviso a las familias. Publica un comunicado o inténtalo de nuevo.")
)
