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
	AuthorName      string
	PublishedAt     time.Time
	Attachments     []Attachment
}

// AnnouncementFilter scopes a feed: one institution, and for most roles only some audiences
type AnnouncementFilter struct {
	InstitutionID string
	// Empty means every audience
	Audiences  []string
	OnlyPublic bool
	Limit      int
}
