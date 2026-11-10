package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/port"
)

const (
	EventAnnouncementPublished = "announcement.published" // {id, slug, institution, public}: a new announcement

	maxAttachments      = 6
	maxImageBytes       = 5 << 20
	maxDocumentBytes    = 10 << 20
	maxAttachmentsBytes = 40 << 20
	slugLength          = 18
	slugAttempts        = 3
)

var (
	slugPattern     = regexp.MustCompile(`^[a-z0-9]{18}$`)
	validKinds      = map[string]bool{domain.KindAnnouncement: true, domain.KindNotice: true, domain.KindAlert: true}
	validAudiences  = map[string]bool{domain.AudienceCommunity: true, domain.AudienceStudents: true, domain.AudienceGuardians: true, domain.AudienceStaff: true}
	allowedFileType = map[string]struct{ kind, ext string }{
		"image/png":       {domain.AttachmentImage, ".png"},
		"image/jpeg":      {domain.AttachmentImage, ".jpg"},
		"image/webp":      {domain.AttachmentImage, ".webp"},
		"application/pdf": {domain.AttachmentDocument, ".pdf"},
	}
	// Roles that see every audience of their institution
	announcementManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true, "secretary": true}
	// Roles that publish
	announcementPublishers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true, "secretary": true, "teacher": true, "tutor": true}
)

// audiencesFor is what each role reads besides the whole community; nil means every audience
func audiencesFor(role string) []string {
	switch {
	case announcementManagers[role]:
		return nil
	case role == "student":
		return []string{domain.AudienceCommunity, domain.AudienceStudents}
	case role == "guardian":
		return []string{domain.AudienceCommunity, domain.AudienceGuardians}
	default:
		return []string{domain.AudienceCommunity, domain.AudienceStaff}
	}
}

func canRead(role, audience string) bool {
	allowed := audiencesFor(role)
	if allowed == nil {
		return true
	}
	for _, a := range allowed {
		if a == audience {
			return true
		}
	}
	return false
}

// AnnouncementService runs announcements, notices and alerts: the panel feed by role, publishing with images and
// PDFs, the public site, and access to the files
type AnnouncementService struct {
	repo   port.AnnouncementRepository
	auth   *AuthService
	store  port.ObjectStore
	live   *LiveService
	logger zerolog.Logger
}

func NewAnnouncementService(repo port.AnnouncementRepository, auth *AuthService, store port.ObjectStore, live *LiveService, logger zerolog.Logger) *AnnouncementService {
	return &AnnouncementService{repo: repo, auth: auth, store: store, live: live, logger: logger}
}

// scope is the institution a panel request works on and the role the person holds there
type scope struct {
	institutionID   string
	institutionSlug string
	role            string
	userID          string
}

// resolveScope picks the institution: the one of the active role, or institutionSlug when the person holds a role
// there; the platform superadmin can open any institution by slug
func (s *AnnouncementService) resolveScope(ctx context.Context, token, institutionSlug string) (scope, error) {
	me, err := s.auth.Me(ctx, token)
	if err != nil {
		return scope{}, err
	}
	if me.ActiveRole != nil && me.ActiveRole.Institution != nil && (institutionSlug == "" || institutionSlug == me.ActiveRole.Institution.Slug) {
		return scope{institutionID: me.ActiveRole.Institution.ID, institutionSlug: me.ActiveRole.Institution.Slug, role: me.ActiveRole.RoleCode, userID: me.User.ID}, nil
	}
	superadmin := false
	for _, r := range me.Roles {
		if r.RoleCode == domain.RoleSuperadmin {
			superadmin = true
		}
		if r.Institution != nil && r.Institution.Slug == institutionSlug {
			return scope{institutionID: r.Institution.ID, institutionSlug: r.Institution.Slug, role: r.RoleCode, userID: me.User.ID}, nil
		}
	}
	if !superadmin {
		return scope{}, domain.ErrForbidden
	}
	if institutionSlug == "" {
		return scope{}, domain.ErrInstitutionRequired
	}
	inst, err := s.repo.InstitutionBySlug(ctx, institutionSlug)
	if err != nil {
		return scope{}, err
	}
	return scope{institutionID: inst.ID, institutionSlug: inst.Slug, role: domain.RoleSuperadmin, userID: me.User.ID}, nil
}

// PanelFeed is the panel list: what the person's role may read in the institution, and whether they may publish
type PanelFeed struct {
	Announcements []domain.Announcement
	CanPublish    bool
}

func (s *AnnouncementService) PanelList(ctx context.Context, token, institutionSlug string) (PanelFeed, error) {
	sc, err := s.resolveScope(ctx, token, institutionSlug)
	if err != nil {
		return PanelFeed{}, err
	}
	items, err := s.repo.List(ctx, domain.AnnouncementFilter{InstitutionID: sc.institutionID, Audiences: audiencesFor(sc.role)})
	if err != nil {
		return PanelFeed{}, err
	}
	return PanelFeed{Announcements: items, CanPublish: announcementPublishers[sc.role]}, nil
}

// PublicList is the public site of an institution (/news)
func (s *AnnouncementService) PublicList(ctx context.Context, institutionSlug string) ([]domain.Announcement, error) {
	inst, err := s.repo.InstitutionBySlug(ctx, institutionSlug)
	if err != nil {
		return nil, err
	}
	return s.repo.List(ctx, domain.AnnouncementFilter{InstitutionID: inst.ID, OnlyPublic: true})
}

// PublicGet is one public announcement (/news/<slug>); internal ones answer not found
func (s *AnnouncementService) PublicGet(ctx context.Context, slug string) (domain.Announcement, error) {
	if !slugPattern.MatchString(slug) {
		return domain.Announcement{}, domain.ErrNotFound
	}
	a, err := s.repo.BySlug(ctx, slug)
	if err != nil {
		return domain.Announcement{}, err
	}
	if !a.IsPublic {
		return domain.Announcement{}, domain.ErrNotFound
	}
	return a, nil
}

// UploadFile is one attached file as received; its type comes from its bytes, never from the client
type UploadFile struct {
	FileName string
	Data     []byte
}

type CreateAnnouncementInput struct {
	InstitutionSlug string
	Kind            string
	Audience        string
	Title           string
	Body            string
	IsPublic        bool
	Files           []UploadFile
}

func cleanText(value string) string { return strings.TrimSpace(value) }

func cleanFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "." || name == "/" || name == "" {
		return "archivo"
	}
	if utf8.RuneCountInString(name) > 200 {
		ext := filepath.Ext(name)
		runes := []rune(strings.TrimSuffix(name, ext))
		name = string(runes[:200-utf8.RuneCountInString(ext)]) + ext
	}
	return name
}

// Create validates, uploads the files to storage and stores the announcement; open pages hear about it live
func (s *AnnouncementService) Create(ctx context.Context, token string, in CreateAnnouncementInput) (domain.Announcement, error) {
	sc, err := s.resolveScope(ctx, token, in.InstitutionSlug)
	if err != nil {
		return domain.Announcement{}, err
	}
	if !announcementPublishers[sc.role] {
		return domain.Announcement{}, domain.ErrForbidden
	}

	title, body := cleanText(in.Title), cleanText(in.Body)
	fields := map[string]string{}
	if !validKinds[in.Kind] {
		fields["kind"] = "Elige el tipo de publicación."
	}
	if !validAudiences[in.Audience] {
		fields["audience"] = "Elige a quién va dirigida."
	}
	if n := utf8.RuneCountInString(title); n < 5 || n > 160 {
		fields["title"] = "El título debe tener entre 5 y 160 caracteres."
	}
	if n := utf8.RuneCountInString(body); n < 10 || n > 5000 {
		fields["body"] = "El detalle debe tener entre 10 y 5000 caracteres."
	}
	attachments, fileProblem := classifyFiles(in.Files)
	if fileProblem != "" {
		fields["files"] = fileProblem
	}
	if len(fields) > 0 {
		return domain.Announcement{}, domain.Validation(fields)
	}

	if len(attachments) > 0 && !s.store.Ready() {
		return domain.Announcement{}, domain.ErrStorageUnavailable
	}
	var stored []string
	cleanup := func() {
		for _, key := range stored {
			if err := s.store.Delete(context.WithoutCancel(ctx), key); err != nil {
				s.logger.Warn().Err(err).Str("key", key).Msg("orphan attachment not removed")
			}
		}
	}
	for i := range attachments {
		att := &attachments[i]
		att.ObjectKey = fmt.Sprintf("announcements/%s/%s%s", sc.institutionID, randomHex(16), allowedFileType[att.ContentType].ext)
		if err := s.store.Put(ctx, att.ObjectKey, att.ContentType, bytes.NewReader(in.Files[i].Data), att.SizeBytes); err != nil {
			cleanup()
			s.logger.Error().Err(err).Msg("attachment upload failed")
			return domain.Announcement{}, domain.ErrStorageUnavailable
		}
		stored = append(stored, att.ObjectKey)
	}

	draft := domain.Announcement{InstitutionID: sc.institutionID, Kind: in.Kind, Audience: in.Audience, Title: title, Body: body, IsPublic: in.IsPublic, Attachments: attachments}
	var created domain.Announcement
	for attempt := 0; attempt < slugAttempts; attempt++ {
		draft.Slug = randomSlug()
		created, err = s.repo.Create(ctx, draft, sc.userID)
		if !errors.Is(err, domain.ErrSlugTaken) {
			break
		}
	}
	if err != nil {
		cleanup()
		return domain.Announcement{}, err
	}

	public := "false"
	if created.IsPublic {
		public = "true"
	}
	s.live.Notify(ctx, EventAnnouncementPublished, map[string]string{"id": created.ID, "slug": created.Slug, "institution": sc.institutionSlug, "public": public})
	return created, nil
}

// classifyFiles checks count, type (by content) and size; positions follow the order received
func classifyFiles(files []UploadFile) ([]domain.Attachment, string) {
	if len(files) > maxAttachments {
		return nil, fmt.Sprintf("Adjunta como máximo %d archivos.", maxAttachments)
	}
	var total int64
	out := make([]domain.Attachment, 0, len(files))
	for i, f := range files {
		size := int64(len(f.Data))
		if size == 0 {
			return nil, fmt.Sprintf("El archivo %s está vacío.", cleanFileName(f.FileName))
		}
		contentType := http.DetectContentType(f.Data)
		if i := strings.Index(contentType, ";"); i >= 0 {
			contentType = contentType[:i]
		}
		meta, ok := allowedFileType[contentType]
		if !ok {
			return nil, fmt.Sprintf("%s no es una imagen (PNG, JPG, WebP) ni un PDF.", cleanFileName(f.FileName))
		}
		limit := int64(maxDocumentBytes)
		if meta.kind == domain.AttachmentImage {
			limit = maxImageBytes
		}
		if size > limit {
			return nil, fmt.Sprintf("%s supera los %d MB.", cleanFileName(f.FileName), limit>>20)
		}
		total += size
		out = append(out, domain.Attachment{Kind: meta.kind, FileName: cleanFileName(f.FileName), ContentType: contentType, SizeBytes: size, Position: i})
	}
	if total > maxAttachmentsBytes {
		return nil, fmt.Sprintf("Los archivos suman más de %d MB.", maxAttachmentsBytes>>20)
	}
	return out, ""
}

// AttachmentFile is a file ready to stream, and whether anyone may cache it (public announcements)
type AttachmentFile struct {
	Body        io.ReadCloser
	Size        int64
	ContentType string
	FileName    string
	Public      bool
}

// OpenAttachment serves a file: anyone for public announcements, otherwise only people of the institution whose
// role may read that audience
func (s *AnnouncementService) OpenAttachment(ctx context.Context, token, announcementID, attachmentID string) (AttachmentFile, error) {
	a, err := s.repo.ByID(ctx, announcementID)
	if err != nil {
		return AttachmentFile{}, err
	}
	var att *domain.Attachment
	for i := range a.Attachments {
		if a.Attachments[i].ID == attachmentID {
			att = &a.Attachments[i]
		}
	}
	if att == nil {
		return AttachmentFile{}, domain.ErrNotFound
	}
	if !a.IsPublic {
		me, err := s.auth.Me(ctx, token)
		if err != nil {
			return AttachmentFile{}, err
		}
		allowed := false
		for _, r := range me.Roles {
			if r.RoleCode == domain.RoleSuperadmin || (r.Institution != nil && r.Institution.ID == a.InstitutionID && canRead(r.RoleCode, a.Audience)) {
				allowed = true
			}
		}
		if !allowed {
			// Same answer as a missing file: nothing tells an outsider the announcement exists
			return AttachmentFile{}, domain.ErrNotFound
		}
	}
	body, size, err := s.store.Get(ctx, att.ObjectKey)
	if err != nil {
		s.logger.Error().Err(err).Str("key", att.ObjectKey).Msg("attachment read failed")
		return AttachmentFile{}, domain.ErrStorageUnavailable
	}
	return AttachmentFile{Body: body, Size: size, ContentType: att.ContentType, FileName: att.FileName, Public: a.IsPublic}, nil
}

const slugAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// randomSlug draws 18 characters of [a-z0-9]
func randomSlug() string {
	var b strings.Builder
	max := big.NewInt(int64(len(slugAlphabet)))
	for i := 0; i < slugLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b.WriteByte(slugAlphabet[n.Int64()])
	}
	return b.String()
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}
