package domain

import "time"

// Institution file: authorizations and the e-learning platform sheet (academic.authorizations / platform_sheets)
const (
	AuthorizationCreation      = "creation"
	AuthorizationAttentionMode = "attention_mode"
	AuthorizationExpansion     = "expansion"
	AuthorizationOther         = "other"

	HostingService   = "hosting"
	HostingOwnServer = "own_server"
	HostingOther     = "other"

	RoleTechnicalLead = "technical_lead"

	OwnerAuthorization = "authorization"
	OwnerPlatformSheet = "platform_sheet"
)

// Authorization is one resolution that authorizes the institution: its creation, a form of attention for a cycle
// (EBA), an expansion of the offer or another act. LevelID empty on an attention_mode one means every cycle
type Authorization struct {
	ID            string
	InstitutionID string
	Kind          string
	LevelID       string
	LevelName     string
	AttentionMode string
	Resolution    string
	IssuedBy      string
	IssuedOn      time.Time
	ExpiresOn     *time.Time
	FileNumber    string
	Notes         string
	// Read-only: PDFs attached (attachments.files, owner_kind authorization)
	Files     int
	UpdatedAt time.Time
}

// InForce reports whether the authorization still holds on day
func (a Authorization) InForce(day time.Time) bool {
	return !a.IssuedOn.After(day) && (a.ExpiresOn == nil || !a.ExpiresOn.Before(day))
}

// PlatformSheet is the technical sheet of the institution's e-learning platform (informe 6.2.1, sections III and IV)
type PlatformSheet struct {
	PlatformName           string
	URL                    string
	Technology             string
	Administrator          string
	AccessProfiles         string
	HostingKind            string
	HostingOther           string
	Provider               string
	Instance               string
	ContractReference      string
	ServiceStatus          string
	ValidUntil             *time.Time
	TechnicalLead          string
	TechnicalLeadSpecialty string
	TechnicalLeadCIP       string
	UpdatedByName          string
	UpdatedAt              time.Time
	// Read-only: hosting evidence attached (owner_kind platform_sheet)
	Files int
}

// Member is one access of the institution's staff: a role held by an account, or an invitation still waiting for
// that email to sign up (the role is granted on sign-up)
type Member struct {
	// user_roles id (account) or invitation id (pending)
	ID         string
	Pending    bool
	UserID     string
	Email      string
	FullName   string
	RoleCode   string
	Since      time.Time
	LastSignIn *time.Time
}

var (
	ErrMembersForbidden = newError(KindForbidden, "members_forbidden", "Solo dirección gestiona los usuarios de la institución.")
	ErrRoleNotGrantable = newError(KindInvalid, "role_not_grantable", "Ese rol no se asigna desde aquí en este tipo de institución.")
	ErrLastDirector     = newError(KindConflict, "last_director", "La institución debe conservar al menos un director.")
	ErrRevokeOwnAccess  = newError(KindConflict, "revoke_own_access", "No puedes quitarte tu propio acceso. Pide a otro director que lo haga.")
	ErrMemberExists     = newError(KindConflict, "member_exists", "Esa persona ya tiene ese rol o una invitación pendiente.")
)

var (
	ErrInstitutionFileForbidden = newError(KindForbidden, "institution_file_forbidden", "Tu rol no puede ver los datos de la institución.")
	ErrInstitutionFileReadOnly  = newError(KindForbidden, "institution_file_read_only", "Solo dirección registra las autorizaciones de la institución.")
	ErrPlatformSheetReadOnly    = newError(KindForbidden, "platform_sheet_read_only", "Solo dirección y el responsable técnico editan la ficha de la plataforma.")
	// The PDFs go first, so no stored file is left without its record
	ErrAuthorizationHasFiles = newError(KindConflict, "authorization_has_files", "La autorización tiene archivos adjuntos. Elimínalos primero.")
	ErrModeNotAuthorized     = newError(KindConflict, "attention_mode_not_authorized", "La institución no tiene autorizada esa forma de atención para este ciclo. Regístrala en Configuración › Institución.")
)
