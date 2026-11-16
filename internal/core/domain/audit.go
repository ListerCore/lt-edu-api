package domain

import "time"

// Audit actions (audit.events.action)
const (
	AuditCreate = "create"
	AuditUpdate = "update"
	AuditDelete = "delete"
	AuditStatus = "status"
	AuditGrant  = "grant"
	AuditRevoke = "revoke"
	AuditOther  = "other"
)

// AuditEvent is one change recorded in the audit log
type AuditEvent struct {
	ID            string
	InstitutionID string
	UserID        string
	UserName      string
	Role          string
	Module        string
	Action        string
	Entity        string
	EntityID      string
	Summary       string
	// Optional detail (changed fields, before and after), stored as JSON
	Changes   map[string]any
	CreatedAt time.Time
}

// AuditFilter narrows the audit list. Before pages backwards: events strictly older than (BeforeAt, BeforeID)
type AuditFilter struct {
	Module   string
	UserID   string
	From, To *time.Time
	BeforeAt *time.Time
	BeforeID string
	Limit    int
}

var ErrAuditForbidden = newError(KindForbidden, "audit_forbidden", "Solo dirección puede ver la auditoría de la institución.")
