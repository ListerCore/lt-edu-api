package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// InstitutionRepository is the institution file in PostgreSQL (academic.authorizations, academic.platform_sheets).
// Lookups that find nothing return domain.ErrNotFound
type InstitutionRepository interface {
	InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error)
	// Authorizations lists the institution's resolutions, newest first, with their attachment counts
	Authorizations(ctx context.Context, institutionID string) ([]domain.Authorization, error)
	Authorization(ctx context.Context, institutionID, id string) (domain.Authorization, error)
	// SaveAuthorization inserts (empty ID) or rewrites one; a level of another institution behaves as missing
	SaveAuthorization(ctx context.Context, a domain.Authorization) (domain.Authorization, error)
	// DeleteAuthorization removes the record (the service refuses while it has files)
	DeleteAuthorization(ctx context.Context, institutionID, id string) error
	// PlatformSheet returns the sheet, or an empty one when the institution has none yet
	PlatformSheet(ctx context.Context, institutionID string) (domain.PlatformSheet, error)
	SavePlatformSheet(ctx context.Context, institutionID string, p domain.PlatformSheet) (domain.PlatformSheet, error)
	// AuthorizedModes maps each level id to the forms of attention authorized for it on day; the "" key holds the
	// forms authorized for every cycle. ok=false when the institution has no attention_mode authorization at all
	AuthorizedModes(ctx context.Context, institutionID string, day time.Time) (modes map[string][]string, ok bool, err error)
}
