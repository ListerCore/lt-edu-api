package port

import (
	"context"

	"api/internal/core/domain"
)

// AuditRepository is audit.events in PostgreSQL (append-only)
type AuditRepository interface {
	InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error)
	// Record stores one event; the user's name is read from iam.users when the event carries none
	Record(ctx context.Context, e domain.AuditEvent) error
	// Events lists the institution's events, newest first, at most f.Limit
	Events(ctx context.Context, institutionID string, f domain.AuditFilter) ([]domain.AuditEvent, error)
}
