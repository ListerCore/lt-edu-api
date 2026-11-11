package domain

import "time"

// Kinds of issued documents (versioned, signed and verifiable)
const DocumentSchedules = "schedules"

// Version states
const (
	VersionPending    = "pending"
	VersionPublished  = "published"
	VersionSuperseded = "superseded"
	VersionCancelled  = "cancelled"
)

// DocumentSigner is someone who signs a kind of document, with the position printed under the name
type DocumentSigner struct {
	ID       string
	StaffID  string
	Name     string
	Position string
	Sort     int
}

// IssuedDocument is a publication followed through its versions; TrackingCode is public and never changes
type IssuedDocument struct {
	ID            string
	InstitutionID string
	Institution   string
	Kind          string
	ScopeKey      string
	Title         string
	TrackingCode  string
	CreatedAt     time.Time
}

// DocumentSignature is a signature a version needs; SignedAt and Code are set once it is signed
type DocumentSignature struct {
	Sort     int
	StaffID  string
	Name     string
	Position string
	SignedAt *time.Time
	Code     string
}

// DocumentVersion is a frozen content of a document and its signatures
type DocumentVersion struct {
	ID               string
	DocumentID       string
	Number           int
	VerificationCode string
	Content          []byte
	ContentHash      string
	Status           string
	Note             string
	CreatedName      string
	CreatedAt        time.Time
	PublishedAt      *time.Time
	Signatures       []DocumentSignature
}

// Signed reports whether every signature is in place
func (v DocumentVersion) Signed() bool {
	for _, s := range v.Signatures {
		if s.SignedAt == nil {
			return false
		}
	}
	return len(v.Signatures) > 0
}

// DocumentEvent is a line of a document's history
type DocumentEvent struct {
	Version int
	Action  string
	Actor   string
	Note    string
	At      time.Time
}

var (
	ErrNoSigners       = newError(KindInvalid, "no_signers", "Primero indica quién firma los horarios (Firmantes).")
	ErrNotSigner       = newError(KindForbidden, "not_signer", "Esta versión no espera tu firma.")
	ErrVersionClosed   = newError(KindConflict, "version_closed", "Esta versión ya no se puede firmar: fue reemplazada o anulada.")
	ErrDocumentsDenied = newError(KindForbidden, "documents_forbidden", "Solo dirección y coordinación académica generan y publican documentos.")
)
