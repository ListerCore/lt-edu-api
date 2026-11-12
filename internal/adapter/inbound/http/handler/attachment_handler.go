package handler

import (
	"io"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/service"
)

// AttachmentHandler serves the generic attachments (PDFs, scans, annexes) of any record: upload (multipart), list,
// stream and delete
type AttachmentHandler struct {
	attachments *service.AttachmentService
	logger      zerolog.Logger
}

func NewAttachmentHandler(attachments *service.AttachmentService, logger zerolog.Logger) *AttachmentHandler {
	return &AttachmentHandler{attachments: attachments, logger: logger}
}

func fileAttachmentJSON(f domain.FileAttachment, canDelete bool) fiber.Map {
	return fiber.Map{
		"id": f.ID, "owner_kind": f.OwnerKind, "owner_id": f.OwnerID, "category": f.Category, "title": f.Title, "description": f.Description,
		"file_name": f.FileName, "content_type": f.ContentType, "size_bytes": f.SizeBytes, "uploaded_name": f.UploadedName,
		"created_at": f.CreatedAt.UTC().Format(time.RFC3339), "can_delete": canDelete,
	}
}

// List: GET ?institution=&owner_kind=&owner_id= → 200 {attachments, can_edit, categories, max_files}
func (h *AttachmentHandler) List(c fiber.Ctx) error {
	list, err := h.attachments.List(c.Context(), bearer(c), c.Query("institution"), c.Query("owner_kind"), c.Query("owner_id"))
	if err != nil {
		return fail(c, h.logger, err)
	}
	items := make([]fiber.Map, len(list.Files))
	for i, f := range list.Files {
		items[i] = fileAttachmentJSON(f.FileAttachment, f.CanDelete)
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	return c.JSON(fiber.Map{"attachments": items, "can_edit": list.CanEdit, "categories": list.Categories, "max_files": list.MaxFiles})
}

// Upload: multipart institution, owner_kind, owner_id, category, title, description and up to 10 "files" →
// 201 {attachments}
func (h *AttachmentHandler) Upload(c fiber.Ctx) error {
	form, err := c.MultipartForm()
	if err != nil {
		return fail(c, h.logger, errInvalidBody)
	}
	value := func(key string) string {
		if v := form.Value[key]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	in := service.AttachmentUpload{InstitutionSlug: value("institution"), OwnerKind: value("owner_kind"), OwnerID: value("owner_id"),
		Category: value("category"), Title: value("title"), Description: value("description")}
	for _, header := range form.File["files"] {
		file, err := header.Open()
		if err != nil {
			return fail(c, h.logger, err)
		}
		// One byte over the limit is enough for the service to refuse it
		data, err := io.ReadAll(io.LimitReader(file, 10<<20+1))
		_ = file.Close()
		if err != nil {
			return fail(c, h.logger, err)
		}
		in.Files = append(in.Files, service.UploadFile{FileName: header.Filename, Data: data})
	}
	created, err := h.attachments.Upload(c.Context(), bearer(c), in)
	if err != nil {
		return fail(c, h.logger, err)
	}
	items := make([]fiber.Map, len(created))
	for i, f := range created {
		items[i] = fileAttachmentJSON(f, true)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"attachments": items})
}

// Delete removes one file and its object: 204
func (h *AttachmentHandler) Delete(c fiber.Ctx) error {
	id := c.Params("id")
	if !uuidPattern.MatchString(id) {
		return fail(c, h.logger, domain.ErrNotFound)
	}
	if err := h.attachments.Delete(c.Context(), bearer(c), c.Query("institution"), id); err != nil {
		return fail(c, h.logger, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// File streams one attachment, never cached. PDFs and images open inline; Word and Excel download
func (h *AttachmentHandler) File(c fiber.Ctx) error {
	id := c.Params("id")
	if !uuidPattern.MatchString(id) {
		return fail(c, h.logger, domain.ErrNotFound)
	}
	file, err := h.attachments.Open(c.Context(), bearer(c), id)
	if err != nil {
		return fail(c, h.logger, err)
	}
	disposition := "attachment"
	if file.Inline {
		disposition = "inline"
	}
	c.Set(fiber.HeaderContentType, file.ContentType)
	c.Set(fiber.HeaderContentDisposition, disposition+"; filename*=UTF-8''"+url.PathEscape(file.FileName))
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	// fasthttp closes the object once it is sent
	return c.SendStream(file.Body, int(file.Size))
}
