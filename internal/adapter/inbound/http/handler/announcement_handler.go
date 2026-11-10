package handler

import (
	"io"
	"net/url"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/service"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// AnnouncementHandler publishes announcements (multipart, with images and PDFs) and serves their files. Reads of
// the feeds go over gRPC (internal/adapter/inbound/grpc/announcement_server.go)
type AnnouncementHandler struct {
	announcements *service.AnnouncementService
	logger        zerolog.Logger
}

func NewAnnouncementHandler(announcements *service.AnnouncementService, logger zerolog.Logger) *AnnouncementHandler {
	return &AnnouncementHandler{announcements: announcements, logger: logger}
}

func announcementJSON(a domain.Announcement) fiber.Map {
	attachments := make([]fiber.Map, len(a.Attachments))
	for i, att := range a.Attachments {
		attachments[i] = fiber.Map{"id": att.ID, "kind": att.Kind, "file_name": att.FileName, "content_type": att.ContentType, "size_bytes": att.SizeBytes, "position": att.Position}
	}
	return fiber.Map{
		"id": a.ID, "slug": a.Slug, "kind": a.Kind, "audience": a.Audience, "title": a.Title, "body": a.Body, "is_public": a.IsPublic,
		"pinned": a.Pinned, "author_name": a.AuthorName, "published_at": a.PublishedAt.UTC().Format(time.RFC3339), "institution_slug": a.InstitutionSlug,
		"attachments": attachments,
	}
}

// Create: multipart/form-data with kind, audience, title, body, is_public, institution and up to 6 "files".
// 201 {announcement}
func (h *AnnouncementHandler) Create(c fiber.Ctx) error {
	form, err := c.MultipartForm()
	if err != nil {
		return fail(c, h.logger, &domain.Error{Kind: domain.KindInvalid, Code: "invalid_body", Message: "La solicitud no es válida."})
	}
	value := func(key string) string {
		if v := form.Value[key]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	var files []service.UploadFile
	for _, header := range form.File["files"] {
		file, err := header.Open()
		if err != nil {
			return fail(c, h.logger, err)
		}
		data, err := io.ReadAll(io.LimitReader(file, 11<<20))
		_ = file.Close()
		if err != nil {
			return fail(c, h.logger, err)
		}
		files = append(files, service.UploadFile{FileName: header.Filename, Data: data})
	}
	created, err := h.announcements.Create(c.Context(), bearer(c), service.CreateAnnouncementInput{
		InstitutionSlug: value("institution"), Kind: value("kind"), Audience: value("audience"), Title: value("title"), Body: value("body"),
		IsPublic: value("is_public") == "true", Files: files,
	})
	if err != nil {
		return fail(c, h.logger, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"announcement": announcementJSON(created)})
}

// Attachment streams one file. Public announcements are cacheable for anyone; internal ones need a session whose
// role may read them, and are never cached
func (h *AnnouncementHandler) Attachment(c fiber.Ctx) error {
	id, attachmentID := c.Params("id"), c.Params("attachmentId")
	if !uuidPattern.MatchString(id) || !uuidPattern.MatchString(attachmentID) {
		return fail(c, h.logger, domain.ErrNotFound)
	}
	file, err := h.announcements.OpenAttachment(c.Context(), bearer(c), id, attachmentID)
	if err != nil {
		return fail(c, h.logger, err)
	}
	c.Set(fiber.HeaderContentType, file.ContentType)
	c.Set(fiber.HeaderContentDisposition, "inline; filename*=UTF-8''"+url.PathEscape(file.FileName))
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	if file.Public {
		c.Set(fiber.HeaderCacheControl, "public, max-age=300")
	} else {
		c.Set(fiber.HeaderCacheControl, "private, no-store")
	}
	// fasthttp closes the object once it is sent
	return c.SendStream(file.Body, int(file.Size))
}
