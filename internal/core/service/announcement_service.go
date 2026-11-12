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
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/port"
)

const (
	EventAnnouncementPublished = "announcement.published" // {id, slug, institution, public}: a new announcement
	EventAnnouncementUpdated   = "announcement.updated"   // {id, slug, institution, public}: edited
	EventAnnouncementDeleted   = "announcement.deleted"   // {id, slug, institution, public}: removed

	maxAttachments      = 6
	maxImageBytes       = 5 << 20
	maxDocumentBytes    = 10 << 20
	maxAttachmentsBytes = 40 << 20
	slugLength          = 18
	slugAttempts        = 3
)

var (
	slugPattern     = regexp.MustCompile(`^[a-z0-9]{18}$`)
	ctaURLPattern   = regexp.MustCompile(`^(/([^/].*)?|https://[^\s]+)$`)
	validCTALabels  = map[string]bool{"Ir": true, "Continuar": true}
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
	sc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, institutionSlug)
	if err != nil {
		return scope{}, err
	}
	return scope{institutionID: sc.institution.ID, institutionSlug: sc.institution.Slug, role: sc.role, userID: sc.userID}, nil
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

// AnnouncementInput is what the panel sends to publish or edit
type AnnouncementInput struct {
	InstitutionSlug string
	Kind            string
	Audience        string
	Title           string
	Body            string
	IsPublic        bool
	// Validity window of the home modal; both empty means not featured
	FeaturedFrom  *time.Time
	FeaturedUntil *time.Time
	CTALabel      string
	CTAURL        string
	// New files, appended after the kept ones
	Files []UploadFile
	// Editing only: attachments to keep, in their new order (the rest are removed)
	Keep []string
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

// validateFields checks everything but the files and returns the cleaned announcement fields. windowChanged says
// whether the validity window is new or edited (only then must it still be open)
func validateFields(in AnnouncementInput, now time.Time, windowChanged bool) (domain.Announcement, map[string]string) {
	a := domain.Announcement{Kind: in.Kind, Audience: in.Audience, Title: cleanText(in.Title), Body: cleanText(in.Body), IsPublic: in.IsPublic}
	fields := map[string]string{}
	if !validKinds[a.Kind] {
		fields["kind"] = "Elige el tipo de publicación."
	}
	if !validAudiences[a.Audience] {
		fields["audience"] = "Elige a quién va dirigida."
	}
	if n := utf8.RuneCountInString(a.Title); n < 5 || n > 160 {
		fields["title"] = "El título debe tener entre 5 y 160 caracteres."
	}
	if n := utf8.RuneCountInString(a.Body); n < 10 || n > 5000 {
		fields["body"] = "El detalle debe tener entre 10 y 5000 caracteres."
	}

	switch {
	case in.FeaturedFrom == nil && in.FeaturedUntil == nil:
		// Not featured: no button either
	case in.FeaturedUntil == nil:
		fields["featured_until"] = "Indica hasta cuándo se muestra en el inicio."
	default:
		from := now
		if in.FeaturedFrom != nil {
			from = *in.FeaturedFrom
		}
		until := *in.FeaturedUntil
		switch {
		case !a.IsPublic:
			fields["featured_until"] = "Solo las publicaciones públicas se muestran en el inicio."
		case !until.After(from):
			fields["featured_until"] = "La vigencia debe terminar después de empezar."
		case windowChanged && !until.After(now):
			fields["featured_until"] = "La vigencia ya terminó; elige una fecha futura."
		case until.Sub(from) > 366*24*time.Hour:
			fields["featured_until"] = "La vigencia no puede pasar de un año."
		}
		a.FeaturedFrom, a.FeaturedUntil = &from, &until
		a.CTALabel = in.CTALabel
		if a.CTALabel == "" {
			a.CTALabel = "Continuar"
		}
		if !validCTALabels[a.CTALabel] {
			fields["cta_label"] = "El botón dice «Ir» o «Continuar»."
		}
		a.CTAURL = strings.TrimSpace(in.CTAURL)
		if a.CTAURL != "" && (len(a.CTAURL) > 500 || !ctaURLPattern.MatchString(a.CTAURL)) {
			fields["cta_url"] = "Usa una ruta del sitio (/admision) o una dirección https://."
		}
	}
	return a, fields
}

func notifyData(a domain.Announcement, institutionSlug string) map[string]string {
	public := "false"
	if a.IsPublic {
		public = "true"
	}
	return map[string]string{"id": a.ID, "slug": a.Slug, "institution": institutionSlug, "public": public}
}

// putFiles uploads new attachments; on failure it removes what it already stored
func (s *AnnouncementService) putFiles(ctx context.Context, institutionID string, files []UploadFile, attachments []domain.Attachment) ([]domain.Attachment, error) {
	if len(attachments) > 0 && !s.store.Ready() {
		return nil, domain.ErrStorageUnavailable
	}
	var stored []string
	for i := range attachments {
		att := &attachments[i]
		att.ObjectKey = fmt.Sprintf("announcements/%s/%s%s", institutionID, randomHex(16), allowedFileType[att.ContentType].ext)
		if err := s.store.Put(ctx, att.ObjectKey, att.ContentType, bytes.NewReader(files[i].Data), att.SizeBytes); err != nil {
			s.removeObjects(ctx, stored)
			s.logger.Error().Err(err).Msg("attachment upload failed")
			return nil, domain.ErrStorageUnavailable
		}
		stored = append(stored, att.ObjectKey)
	}
	return attachments, nil
}

// removeObjects deletes stored files whose rows are gone; a failure only leaves an orphan, logged
func (s *AnnouncementService) removeObjects(ctx context.Context, keys []string) {
	for _, key := range keys {
		if err := s.store.Delete(context.WithoutCancel(ctx), key); err != nil {
			s.logger.Warn().Err(err).Str("key", key).Msg("orphan attachment not removed")
		}
	}
}

func objectKeys(attachments []domain.Attachment) []string {
	keys := make([]string, len(attachments))
	for i, a := range attachments {
		keys[i] = a.ObjectKey
	}
	return keys
}

// Create validates, uploads the files to storage and stores the announcement; open pages hear about it live
func (s *AnnouncementService) Create(ctx context.Context, token string, in AnnouncementInput) (domain.Announcement, error) {
	sc, err := s.resolveScope(ctx, token, in.InstitutionSlug)
	if err != nil {
		return domain.Announcement{}, err
	}
	if !announcementPublishers[sc.role] {
		return domain.Announcement{}, domain.ErrForbidden
	}
	draft, fields := validateFields(in, time.Now(), true)
	attachments, fileProblem := classifyFiles(in.Files, 0, 0)
	if fileProblem != "" {
		fields["files"] = fileProblem
	}
	if len(fields) > 0 {
		return domain.Announcement{}, domain.Validation(fields)
	}
	if attachments, err = s.putFiles(ctx, sc.institutionID, in.Files, attachments); err != nil {
		return domain.Announcement{}, err
	}

	draft.InstitutionID = sc.institutionID
	draft.Attachments = attachments
	var created domain.Announcement
	for attempt := 0; attempt < slugAttempts; attempt++ {
		draft.Slug = randomSlug()
		created, err = s.repo.Create(ctx, draft, sc.userID)
		if !errors.Is(err, domain.ErrSlugTaken) {
			break
		}
	}
	if err != nil {
		s.removeObjects(ctx, objectKeys(attachments))
		return domain.Announcement{}, err
	}
	s.live.Notify(ctx, EventAnnouncementPublished, notifyData(created, sc.institutionSlug))
	return created, nil
}

// NoticeInput is an internal announcement another module publishes for the person acting there (an APAFA
// convocatoria, its change or its acta). The calling service has already checked the person's role
type NoticeInput struct {
	InstitutionID   string
	InstitutionSlug string
	AuthorID        string
	Kind            string
	Audience        string
	Title           string
	Body            string
}

// PublishNotice stores an internal announcement (no files, not featured) with the same field rules as the panel
// form, so it lands in Comunicados, the header ticker and every open page live
func (s *AnnouncementService) PublishNotice(ctx context.Context, in NoticeInput) (domain.Announcement, error) {
	draft, fields := validateFields(AnnouncementInput{Kind: in.Kind, Audience: in.Audience, Title: in.Title, Body: in.Body}, time.Now(), false)
	if len(fields) > 0 {
		return domain.Announcement{}, domain.Validation(fields)
	}
	draft.InstitutionID = in.InstitutionID
	var created domain.Announcement
	var err error
	for attempt := 0; attempt < slugAttempts; attempt++ {
		draft.Slug = randomSlug()
		created, err = s.repo.Create(ctx, draft, in.AuthorID)
		if !errors.Is(err, domain.ErrSlugTaken) {
			break
		}
	}
	if err != nil {
		return domain.Announcement{}, err
	}
	s.live.Notify(ctx, EventAnnouncementPublished, notifyData(created, in.InstitutionSlug))
	return created, nil
}

// canEdit: those who see the whole institution edit anything; teachers and tutors only what they published
func canEdit(sc scope, a domain.Announcement) bool {
	return announcementManagers[sc.role] || (announcementPublishers[sc.role] && a.AuthorID != "" && a.AuthorID == sc.userID)
}

// editable loads an announcement of the person's institution that they may change
func (s *AnnouncementService) editable(ctx context.Context, token, institutionSlug, id string) (scope, domain.Announcement, error) {
	sc, err := s.resolveScope(ctx, token, institutionSlug)
	if err != nil {
		return sc, domain.Announcement{}, err
	}
	a, err := s.repo.ByID(ctx, id)
	if err != nil {
		return sc, a, err
	}
	if a.InstitutionID != sc.institutionID || !canRead(sc.role, a.Audience) {
		return sc, a, domain.ErrNotFound
	}
	if !canEdit(sc, a) {
		return sc, a, domain.ErrForbidden
	}
	return sc, a, nil
}

// Update edits the fields and the attachments (keep some, in a new order, and add new ones)
func (s *AnnouncementService) Update(ctx context.Context, token, id string, in AnnouncementInput) (domain.Announcement, error) {
	sc, current, err := s.editable(ctx, token, in.InstitutionSlug, id)
	if err != nil {
		return domain.Announcement{}, err
	}
	windowChanged := !sameTime(in.FeaturedFrom, current.FeaturedFrom) || !sameTime(in.FeaturedUntil, current.FeaturedUntil)
	draft, fields := validateFields(in, time.Now(), windowChanged)

	existing := map[string]domain.Attachment{}
	for _, att := range current.Attachments {
		existing[att.ID] = att
	}
	var keptBytes int64
	seen := map[string]bool{}
	for _, keepID := range in.Keep {
		att, ok := existing[keepID]
		if !ok || seen[keepID] {
			fields["files"] = "Uno de los archivos ya no existe. Recarga la página."
			break
		}
		seen[keepID] = true
		keptBytes += att.SizeBytes
	}
	added, fileProblem := classifyFiles(in.Files, len(in.Keep), keptBytes)
	if fileProblem != "" {
		fields["files"] = fileProblem
	}
	if len(fields) > 0 {
		return domain.Announcement{}, domain.Validation(fields)
	}
	if added, err = s.putFiles(ctx, sc.institutionID, in.Files, added); err != nil {
		return domain.Announcement{}, err
	}

	draft.ID = current.ID
	updated, removed, err := s.repo.Update(ctx, draft, in.Keep, added)
	if err != nil {
		s.removeObjects(ctx, objectKeys(added))
		return domain.Announcement{}, err
	}
	s.removeObjects(ctx, removed)
	s.live.Notify(ctx, EventAnnouncementUpdated, notifyData(updated, sc.institutionSlug))
	return updated, nil
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// Delete removes the announcement and its files
func (s *AnnouncementService) Delete(ctx context.Context, token, institutionSlug, id string) error {
	sc, current, err := s.editable(ctx, token, institutionSlug, id)
	if err != nil {
		return err
	}
	keys, err := s.repo.Delete(ctx, current.ID)
	if err != nil {
		return err
	}
	s.removeObjects(ctx, keys)
	s.live.Notify(ctx, EventAnnouncementDeleted, notifyData(current, sc.institutionSlug))
	return nil
}

// PanelGet is one announcement for the panel (detail and edit page), and whether the person may change it
func (s *AnnouncementService) PanelGet(ctx context.Context, token, institutionSlug, slug string) (domain.Announcement, bool, error) {
	if !slugPattern.MatchString(slug) {
		return domain.Announcement{}, false, domain.ErrNotFound
	}
	sc, err := s.resolveScope(ctx, token, institutionSlug)
	if err != nil {
		return domain.Announcement{}, false, err
	}
	a, err := s.repo.BySlug(ctx, slug)
	if err != nil {
		return a, false, err
	}
	if a.InstitutionID != sc.institutionID || !canRead(sc.role, a.Audience) {
		return domain.Announcement{}, false, domain.ErrNotFound
	}
	return a, canEdit(sc, a), nil
}

// PublicFeatured is what the home modal shows: public announcements within their validity window, newest first
func (s *AnnouncementService) PublicFeatured(ctx context.Context, institutionSlug string) ([]domain.Announcement, error) {
	inst, err := s.repo.InstitutionBySlug(ctx, institutionSlug)
	if err != nil {
		return nil, err
	}
	return s.repo.Featured(ctx, inst.ID)
}

// classifyFiles checks count, type (by content) and size of new files; positions continue after the kept ones,
// and the limits count what is kept too
func classifyFiles(files []UploadFile, keptCount int, keptBytes int64) ([]domain.Attachment, string) {
	if keptCount+len(files) > maxAttachments {
		return nil, fmt.Sprintf("Adjunta como máximo %d archivos.", maxAttachments)
	}
	total := keptBytes
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
		out = append(out, domain.Attachment{Kind: meta.kind, FileName: cleanFileName(f.FileName), ContentType: contentType, SizeBytes: size, Position: keptCount + i})
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
