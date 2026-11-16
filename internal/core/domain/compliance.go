package domain

import "time"

// DRE compliance (phase E8 of docs/ceba/PLAN.md): the environment and equipment of the virtual tutors, their
// connectivity and access checks, the verification matrices of the two technical reports and the availability of
// the platform

// Attachment owners of phase E8
const (
	OwnerEquipmentRoom   = "equipment_room"
	OwnerEquipmentDevice = "equipment_device"
	OwnerEquipmentCheck  = "equipment_check"
	OwnerComplianceItem  = "compliance_item"
)

// Reports and item states
const (
	ReportPlatform  = "platform"
	ReportEquipment = "equipment"

	ItemPending       = "pending"
	ItemVerified      = "verified"
	ItemNotApplicable = "not_applicable"
)

type EquipmentRoom struct {
	ID           string
	Name         string
	Location     string
	AreaM2       *float64
	Workstations int
	Furniture    string
	PowerSupply  string
	Notes        string
	Devices      int
	Files        int
}

type Device struct {
	ID         string
	Code       string
	Kind       string
	Brand      string
	Model      string
	Specs      string
	RoomID     string
	RoomName   string
	Purpose    string
	StaffID    string
	StaffName  string
	AssignedTo string
	Status     string
	Notes      string
	Files      int
	// Latest passed platform-access check from it (nil: none)
	LastAccess *time.Time
}

type EquipmentCheck struct {
	ID            string
	Kind          string
	RoomID        string
	RoomName      string
	DeviceID      string
	DeviceCode    string
	CheckedOn     time.Time
	Provider      string
	LinkKind      string
	DownloadMbps  *float64
	UploadMbps    *float64
	LatencyMs     *int
	Profile       string
	Passed        bool
	Result        string
	CheckedByName string
	Files         int
}

type ComplianceReport struct {
	Kind          string
	Number        string
	FileNumber    string
	City          string
	Location      string
	EvaluatedOn   *time.Time
	GeneralResult string
	Limitations   string
	Conclusion    string
	UpdatedByName string
	UpdatedAt     *time.Time
}

type ComplianceItem struct {
	ID             string
	Report         string
	Section        string
	Code           string
	Position       int
	Element        string
	Module         string
	Route          string
	Annex          string
	Folio          string
	Status         string
	Notes          string
	VerifiedByName string
	VerifiedAt     *time.Time
	Files          int
}

type UptimeCheck struct {
	URL        string
	CheckedAt  time.Time
	StatusCode *int
	ResponseMs *int
	OK         bool
	Error      string
}

// UptimeTarget is an institution with a platform URL to watch
type UptimeTarget struct {
	InstitutionID string
	URL           string
}

var (
	ErrComplianceForbidden = newError(KindForbidden, "compliance_forbidden", "Solo dirección, coordinación y el responsable técnico ven el cumplimiento.")
	ErrComplianceReadOnly  = newError(KindForbidden, "compliance_read_only", "Solo dirección y el responsable técnico registran equipos y evidencias.")
	ErrVerifyTechLead      = newError(KindForbidden, "verify_tech_lead", "Solo el responsable técnico marca un elemento como verificado.")
	ErrNoEvidence          = newError(KindConflict, "no_evidence", "Sube al menos una evidencia antes de marcarlo como verificado.")
	ErrDeviceCodeTaken     = newError(KindConflict, "device_code_taken", "Ya hay un equipo con ese código.")
	ErrRoomNameTaken       = newError(KindConflict, "room_name_taken", "Ya hay un ambiente con ese nombre.")
	ErrNoPlatformURL       = newError(KindConflict, "no_platform_url", "Registra la URL de la plataforma en la ficha de la institución.")
)
