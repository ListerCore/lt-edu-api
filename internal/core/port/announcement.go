package port

import (
	"context"
	"io"

	"api/internal/core/domain"
)

// AnnouncementRepository is comms.* in PostgreSQL. Lookups that find nothing return domain.ErrNotFound
type AnnouncementRepository interface {
	InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error)
	// List returns the feed with attachments, pinned first and newest first
	List(ctx context.Context, filter domain.AnnouncementFilter) ([]domain.Announcement, error)
	BySlug(ctx context.Context, slug string) (domain.Announcement, error)
	ByID(ctx context.Context, id string) (domain.Announcement, error)
	// Create stores the announcement and its attachments in one transaction; a taken slug is domain.ErrSlugTaken
	Create(ctx context.Context, a domain.Announcement, authorID string) (domain.Announcement, error)
	// Update rewrites the fields and the attachments: keep (existing ids, in their new order) then added (new rows,
	// positions already set). Returns the storage keys of the attachments that were dropped
	Update(ctx context.Context, a domain.Announcement, keep []string, added []domain.Attachment) (domain.Announcement, []string, error)
	// Delete removes the announcement and its attachment rows; returns their storage keys
	Delete(ctx context.Context, id string) ([]string, error)
	// Featured lists the public announcements whose validity window contains now, newest first
	Featured(ctx context.Context, institutionID string) ([]domain.Announcement, error)
}

// ObjectStore keeps file bytes (AIStor)
type ObjectStore interface {
	Ready() bool
	Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
}
