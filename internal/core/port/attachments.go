package port

import (
	"context"

	"api/internal/core/domain"
)

// AttachmentRepository is attachments.* in PostgreSQL. Lookups that find nothing return domain.ErrNotFound
type AttachmentRepository interface {
	InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error)
	// Owner looks the owner record up in its own table. Kinds without a table answer Checked=false and no error;
	// a missing record is domain.ErrNotFound
	Owner(ctx context.Context, ownerKind, ownerID string) (domain.AttachmentOwner, error)
	// StudentsForEmail are the active students of the institution whose own email or whose guardian's email it is
	StudentsForEmail(ctx context.Context, institutionID, email string) ([]string, error)
	// List returns the files of one owner, newest first
	List(ctx context.Context, institutionID, ownerKind, ownerID string) ([]domain.FileAttachment, error)
	// Create stores the rows in one transaction; when the owner would pass max files it stores nothing and answers
	// domain.ErrAttachmentsFull (the count is taken under a lock, so two uploads at once cannot both pass)
	Create(ctx context.Context, files []domain.FileAttachment, max int) ([]domain.FileAttachment, error)
	ByID(ctx context.Context, id string) (domain.FileAttachment, error)
	// Delete removes the row; the caller removes the object from storage
	Delete(ctx context.Context, id string) error
}
