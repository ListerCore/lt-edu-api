package domain

import "time"

// Owner kinds of attachments.files: the record a file hangs on. Each one maps to a panel module whose
// permissions govern who reads and who changes its files (service/attachment_service.go)
const (
	OwnerEnrollment              = "enrollment"
	OwnerStudent                 = "student"
	OwnerStaff                   = "staff"
	OwnerTutoring                = "tutoring"
	OwnerDiscipline              = "discipline"
	OwnerPsychology              = "psychology"
	OwnerIndividualPlan          = "individual_plan"
	OwnerTherapy                 = "therapy"
	OwnerProgressReport          = "progress_report"
	OwnerInclusionSupport        = "inclusion_support"
	OwnerPeriod                  = "period"
	OwnerClosing                 = "closing"
	OwnerInternship              = "internship"
	OwnerDegree                  = "degree"
	OwnerAttendanceJustification = "attendance_justification"
	OwnerApafaMeeting            = "apafa_meeting"
)

// FileAttachment is one row of attachments.files
type FileAttachment struct {
	ID            string
	InstitutionID string
	OwnerKind     string
	OwnerID       string
	Category      string
	Title         string
	Description   string
	ObjectKey     string
	FileName      string
	ContentType   string
	SizeBytes     int64
	UploadedBy    string
	UploadedName  string
	CreatedAt     time.Time
}

// AttachmentOwner is what the repository knows about an owner record: its institution and, when the record is
// about one student, that student (families only see the files of their own children)
type AttachmentOwner struct {
	InstitutionID string
	StudentID     string
	// False when the owner kind has no table yet: only the id format was checked
	Checked bool
	// A draft families may not open yet (an APAFA meeting not convened)
	FamilyHidden bool
}

var (
	ErrAttachmentsFull     = newError(KindConflict, "attachments_full", "Este registro ya tiene el máximo de archivos. Elimina alguno para subir otro.")
	ErrAttachmentOwner     = newError(KindNotFound, "attachment_owner_not_found", "No encontramos el registro al que quieres adjuntar archivos.")
	ErrAttachmentForbidden = newError(KindForbidden, "attachments_forbidden", "Tu rol no puede ver los archivos de este registro.")
	ErrAttachmentReadOnly  = newError(KindForbidden, "attachments_read_only", "Tu rol puede ver estos archivos, pero no subirlos ni eliminarlos.")
)
