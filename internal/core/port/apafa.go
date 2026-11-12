package port

import (
	"context"

	"api/internal/core/domain"
)

// ApafaRepository is apafa.* in PostgreSQL. Lookups that find nothing return domain.ErrNotFound
type ApafaRepository interface {
	InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error)
	// Management is the institution's management type (public | private; private without a profile)
	Management(ctx context.Context, institutionID string) (string, error)
	// Meetings lists the meetings of the institution, newest first; drafts=false leaves out the ones never convened
	Meetings(ctx context.Context, institutionID string, drafts bool) ([]domain.ApafaMeeting, error)
	Meeting(ctx context.Context, institutionID, id string) (domain.ApafaMeeting, error)
	CreateMeeting(ctx context.Context, m domain.ApafaMeeting) (domain.ApafaMeeting, error)
	// UpdateMeeting rewrites every editable column of the meeting (status, notes, notice links included)
	UpdateMeeting(ctx context.Context, m domain.ApafaMeeting) (domain.ApafaMeeting, error)
	DeleteMeeting(ctx context.Context, institutionID, id string) error
	Board(ctx context.Context, institutionID string) ([]domain.ApafaBoardMember, error)
	SaveBoardMember(ctx context.Context, b domain.ApafaBoardMember) (domain.ApafaBoardMember, error)
	DeleteBoardMember(ctx context.Context, institutionID, id string) error
	// Fees sums the APAFA fee charges (concept apafa) of the periods not closed yet
	Fees(ctx context.Context, institutionID string) (domain.ApafaFees, error)
}
