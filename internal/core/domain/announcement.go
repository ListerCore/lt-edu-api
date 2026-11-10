package domain

import (
	"errors"
	"time"
)

// ErrSlugTaken is internal: the random slug collided, the service draws another
var ErrSlugTaken = errors.New("announcement slug taken")

// Announcement kinds and audiences (comms.announcements)
const (
	KindAnnouncement = "announcement"
	KindNotice       = "notice"
	KindAlert        = "alert"

	AudienceCommunity = "community"
	AudienceStudents  = "students"
	AudienceGuardians = "guardians"
	AudienceStaff     = "staff"

	AttachmentImage    = "image"
	AttachmentDocument = "document"
)

type Attachment struct {
	ID          string
	Kind        string
	ObjectKey   string
	FileName    string
	ContentType string
	SizeBytes   int64
	Position    int
}

type Announcement struct {
	ID              string
	InstitutionID   string
	InstitutionSlug string
	Slug            string
	Kind            string
	Audience        string
	Title           string
	Body            string
	IsPublic        bool
	Pinned          bool
	AuthorID        string
	AuthorName      string
	PublishedAt     time.Time
	UpdatedAt       time.Time
	// Validity window: featured in the home modal while now is within it (public announcements only)
	FeaturedFrom  *time.Time
	FeaturedUntil *time.Time
	// Button of the modal: "Ir" or "Continuar", leading to CTAURL (its own page when empty)
	CTALabel    string
	CTAURL      string
	Attachments []Attachment
}

// FeaturedAt reports whether the announcement is in the home modal at t
func (a Announcement) FeaturedAt(t time.Time) bool {
	return a.IsPublic && a.FeaturedFrom != nil && a.FeaturedUntil != nil && !t.Before(*a.FeaturedFrom) && t.Before(*a.FeaturedUntil)
}

// AnnouncementFilter scopes a feed: one institution, and for most roles only some audiences
type AnnouncementFilter struct {
	InstitutionID string
	// Empty means every audience
	Audiences  []string
	OnlyPublic bool
	Limit      int
}
