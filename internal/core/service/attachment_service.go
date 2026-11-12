package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/port"
)

const (
	maxFileAttachmentBytes = 10 << 20
	// Files one owner record may hold, and files one upload may carry
	maxFilesPerOwner  = 20
	maxFilesPerUpload = 10
	// One upload stays under the HTTP body limit (cmd/server) and joseph-app's server action limit
	maxUploadBytes = 40 << 20
	maxTitleRunes  = 160
	maxDescRunes   = 500

	mimeDOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimeXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
)

// fileType is what a sniffed type means for storage and for the browser: inline types open in a tab, the rest
// download
type fileType struct {
	ext    string
	inline bool
}

// attachmentIDPattern is the canonical lowercase uuid an owner id must have
var attachmentIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var attachmentTypes = map[string]fileType{
	"application/pdf": {".pdf", true},
	"image/png":       {".png", true},
	"image/jpeg":      {".jpg", true},
	"image/webp":      {".webp", true},
	mimeDOCX:          {".docx", false},
	mimeXLSX:          {".xlsx", false},
}

// ownerSpec ties an owner kind to the panel module that governs it. read / write mirror joseph-app's rolePermissions
// (features/protected/dashboard/data/permissions.ts: view vs create/update) with the API's role codes; the platform
// superadmin always passes. familyScoped: students and guardians only reach owners about one of their own children
type ownerSpec struct {
	module       string
	read, write  map[string]bool
	familyScoped bool
	categories   []string
}

func roleSet(codes ...string) map[string]bool {
	m := make(map[string]bool, len(codes))
	for _, c := range codes {
		m[c] = true
	}
	return m
}

const (
	rDirector     = "director"
	rCoordinator  = "academic_coordinator"
	rSecretary    = "secretary"
	rTeacher      = "teacher"
	rTutor        = "tutor"
	rAssistant    = "assistant"
	rPsychologist = "psychologist"
	rTreasurer    = "treasurer"
	rStudent      = "student"
	rGuardian     = "guardian"
)

var ownerSpecs = map[string]ownerSpec{
	domain.OwnerEnrollment: {module: "enrollment",
		read: roleSet(rDirector, rSecretary, rStudent, rGuardian), write: roleSet(rDirector, rSecretary), familyScoped: true,
		categories: []string{"dni", "birth_certificate", "study_certificate", "transfer_certificate", "enrollment_form", "medical_report", "disability_certificate", "photo", "other"}},
	domain.OwnerStudent: {module: "students",
		read: roleSet(rDirector, rCoordinator, rSecretary, rTeacher, rTutor, rAssistant, rPsychologist, rTreasurer, rStudent, rGuardian), write: roleSet(rDirector, rSecretary), familyScoped: true,
		categories: []string{"dni", "birth_certificate", "study_certificate", "medical_report", "disability_certificate", "vaccination_card", "photo", "other"}},
	domain.OwnerStaff: {module: "staff",
		read: roleSet(rDirector, rCoordinator, rSecretary), write: roleSet(rDirector),
		categories: []string{"dni", "cv", "professional_title", "contract", "resolution", "certificate", "criminal_record", "medical_report", "other"}},
	domain.OwnerTutoring: {module: "tutoring",
		read: roleSet(rDirector, rCoordinator, rTutor, rPsychologist, rStudent, rGuardian), write: roleSet(rDirector, rTutor), familyScoped: true,
		categories: []string{"minutes", "report", "agreement", "evidence", "other"}},
	domain.OwnerDiscipline: {module: "discipline",
		read: roleSet(rDirector, rCoordinator, rTeacher, rTutor, rAssistant, rPsychologist, rStudent, rGuardian), write: roleSet(rDirector, rTeacher, rTutor, rAssistant), familyScoped: true,
		categories: []string{"report", "minutes", "evidence", "resolution", "agreement", "other"}},
	// Confidential: only the psychologist (and the platform superadmin), like the psychology module itself
	domain.OwnerPsychology: {module: "psychology",
		read: roleSet(rPsychologist), write: roleSet(rPsychologist),
		categories: []string{"report", "evaluation", "consent", "referral", "other"}},
	domain.OwnerIndividualPlan: {module: "individual-plans",
		read: roleSet(rDirector, rCoordinator, rTeacher, rTutor, rPsychologist, RoleSpecialEd, rGuardian), write: roleSet(rDirector, rCoordinator, rTeacher, rTutor, RoleSpecialEd), familyScoped: true,
		categories: []string{"plan", "evaluation", "report", "medical_report", "other"}},
	domain.OwnerTherapy: {module: "therapies",
		read: roleSet(rDirector, rCoordinator, rTeacher, rTutor, rPsychologist, RoleSpecialEd, rGuardian), write: roleSet(rDirector, rPsychologist, RoleSpecialEd), familyScoped: true,
		categories: []string{"report", "evaluation", "evidence", "medical_report", "other"}},
	domain.OwnerProgressReport: {module: "progress-reports",
		read: roleSet(rDirector, rCoordinator, rTeacher, rTutor, RoleSpecialEd, rGuardian), write: roleSet(rDirector, rCoordinator, rTeacher, rTutor, RoleSpecialEd), familyScoped: true,
		categories: []string{"report", "evidence", "other"}},
	domain.OwnerInclusionSupport: {module: "inclusion-support",
		read: roleSet(rDirector, rCoordinator, rPsychologist, RoleSpecialEd), write: roleSet(rDirector, rCoordinator, rPsychologist, RoleSpecialEd),
		categories: []string{"report", "plan", "evaluation", "minutes", "other"}},
	domain.OwnerPeriod: {module: "periods",
		read: roleSet(rDirector, rCoordinator, rSecretary), write: roleSet(rDirector, rCoordinator),
		categories: []string{"resolution", "annex", "plan", "calendar", "other"}},
	domain.OwnerClosing: {module: "closing",
		read: roleSet(rDirector, rCoordinator, rSecretary), write: roleSet(rDirector, rCoordinator),
		categories: []string{"minutes", "resolution", "annex", "report", "other"}},
	domain.OwnerInternship: {module: "internships",
		read: roleSet(rDirector, rCoordinator, rTeacher, rTutor, rStudent), write: roleSet(rDirector, rCoordinator, rTeacher, rTutor), familyScoped: true,
		categories: []string{"agreement", "plan", "report", "evaluation", "certificate", "evidence", "other"}},
	domain.OwnerDegree: {module: "degrees",
		read: roleSet(rDirector, rCoordinator, rSecretary), write: roleSet(rDirector, rSecretary),
		categories: []string{"thesis", "minutes", "resolution", "certificate", "dni", "payment_receipt", "other"}},
	// Families justify their own children's absences, so they may attach the medical certificate too
	domain.OwnerAttendanceJustification: {module: "student-attendance",
		read: roleSet(rDirector, rCoordinator, rSecretary, rTeacher, rTutor, rAssistant, rStudent, rGuardian), write: roleSet(rDirector, rTeacher, rTutor, rAssistant, rStudent, rGuardian), familyScoped: true,
		categories: []string{"medical_report", "certificate", "evidence", "other"}},
	// APAFA (parents' association) meetings: every guardian of the institution reads them once the meeting is
	// convened (drafts are FamilyHidden); coordinación reads, dirección and secretaría manage (service/apafa_service.go)
	domain.OwnerApafaMeeting: {module: "apafa",
		read: roleSet(rDirector, rCoordinator, rSecretary, rGuardian), write: roleSet(rDirector, rSecretary),
		categories: []string{"minutes", "agenda", "report", "financial_statement", "attendance_list", "other"}},
}

func isFamily(role string) bool { return role == rStudent || role == rGuardian }

// canReadOwner: the role views the module and, for families on student records, the student is one of theirs
func canReadOwner(role string, spec ownerSpec, owner domain.AttachmentOwner, children []string) bool {
	return ownerAllows(role, spec, spec.read, owner, children)
}

// canWriteOwner: the role creates/updates in the module, with the same family restriction
func canWriteOwner(role string, spec ownerSpec, owner domain.AttachmentOwner, children []string) bool {
	return ownerAllows(role, spec, spec.write, owner, children)
}

func ownerAllows(role string, spec ownerSpec, set map[string]bool, owner domain.AttachmentOwner, children []string) bool {
	if role == domain.RoleSuperadmin {
		return true
	}
	if !set[role] {
		return false
	}
	if isFamily(role) && owner.FamilyHidden {
		return false
	}
	if isFamily(role) && spec.familyScoped {
		// An owner whose student is unknown (no table yet) never opens to families
		return owner.StudentID != "" && slices.Contains(children, owner.StudentID)
	}
	return true
}

// canDeleteFile: writers delete any file of the owner; families only what they uploaded themselves
func canDeleteFile(role, userID string, spec ownerSpec, owner domain.AttachmentOwner, children []string, f domain.FileAttachment) bool {
	if !canWriteOwner(role, spec, owner, children) {
		return false
	}
	return !isFamily(role) || (f.UploadedBy != "" && f.UploadedBy == userID)
}

// AttachmentService keeps the files (PDFs, scans, annexes) hung on any record of an institution: upload, list,
// stream and delete, each checked against the module of the owner
type AttachmentService struct {
	repo   port.AttachmentRepository
	auth   *AuthService
	store  port.ObjectStore
	logger zerolog.Logger
}

func NewAttachmentService(repo port.AttachmentRepository, auth *AuthService, store port.ObjectStore, logger zerolog.Logger) *AttachmentService {
	return &AttachmentService{repo: repo, auth: auth, store: store, logger: logger}
}

// attachmentScope is the person on one institution: role, account and, for families, their children
type attachmentScope struct {
	institutionID string
	role          string
	userID        string
	userName      string
	children      []string
}

func (s *AttachmentService) scope(ctx context.Context, token, institutionSlug string) (attachmentScope, error) {
	isc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, institutionSlug)
	if err != nil {
		return attachmentScope{}, err
	}
	me, err := s.auth.Me(ctx, token)
	if err != nil {
		return attachmentScope{}, err
	}
	sc := attachmentScope{institutionID: isc.institution.ID, role: isc.role, userID: isc.userID, userName: uploaderName(me.User)}
	if isFamily(sc.role) {
		if sc.children, err = s.familyStudents(ctx, sc.institutionID, me.User.Email); err != nil {
			return sc, err
		}
	}
	return sc, nil
}

func (s *AttachmentService) familyStudents(ctx context.Context, institutionID, email string) ([]string, error) {
	if email == "" {
		return []string{}, nil
	}
	return s.repo.StudentsForEmail(ctx, institutionID, email)
}

func uploaderName(u domain.User) string {
	if name := strings.TrimSpace(u.FullName); name != "" {
		return name
	}
	return u.Email
}

// owner validates kind and id and loads the owner record of the person's institution
func (s *AttachmentService) owner(ctx context.Context, institutionID, kind, id string) (ownerSpec, domain.AttachmentOwner, error) {
	spec, ok := ownerSpecs[kind]
	if !ok {
		return spec, domain.AttachmentOwner{}, domain.Validation(map[string]string{"owner_kind": "Tipo de registro no válido."})
	}
	if !attachmentIDPattern.MatchString(id) {
		return spec, domain.AttachmentOwner{}, domain.ErrAttachmentOwner
	}
	owner, err := s.repo.Owner(ctx, kind, id)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return spec, owner, domain.ErrAttachmentOwner
	case err != nil:
		return spec, owner, err
	}
	if owner.Checked && owner.InstitutionID != institutionID {
		// Someone else's record looks the same as a missing one
		return spec, owner, domain.ErrAttachmentOwner
	}
	return spec, owner, nil
}

// AttachmentEntry is a listed file and whether the person may delete it
type AttachmentEntry struct {
	domain.FileAttachment
	CanDelete bool
}

// AttachmentList is what the panel shows for one owner
type AttachmentList struct {
	Files      []AttachmentEntry
	CanEdit    bool
	Categories []string
	MaxFiles   int
}

// List returns the files of one owner when the person's role may view its module
func (s *AttachmentService) List(ctx context.Context, token, institutionSlug, ownerKind, ownerID string) (AttachmentList, error) {
	sc, err := s.scope(ctx, token, institutionSlug)
	if err != nil {
		return AttachmentList{}, err
	}
	spec, owner, err := s.owner(ctx, sc.institutionID, ownerKind, ownerID)
	if err != nil {
		return AttachmentList{}, err
	}
	if !canReadOwner(sc.role, spec, owner, sc.children) {
		return AttachmentList{}, domain.ErrAttachmentForbidden
	}
	files, err := s.repo.List(ctx, sc.institutionID, ownerKind, ownerID)
	if err != nil {
		return AttachmentList{}, err
	}
	out := AttachmentList{Files: make([]AttachmentEntry, len(files)), CanEdit: canWriteOwner(sc.role, spec, owner, sc.children), Categories: spec.categories, MaxFiles: maxFilesPerOwner}
	for i, f := range files {
		out.Files[i] = AttachmentEntry{FileAttachment: f, CanDelete: canDeleteFile(sc.role, sc.userID, spec, owner, sc.children, f)}
	}
	return out, nil
}

// AttachmentUpload is what the panel sends: the owner, one category, an optional title and description for every
// file of the upload, and the files
type AttachmentUpload struct {
	InstitutionSlug string
	OwnerKind       string
	OwnerID         string
	Category        string
	Title           string
	Description     string
	Files           []UploadFile
}

// sniffType reads the type from the bytes: PDF and images from their magic numbers, DOCX / XLSX from the parts
// inside the OOXML zip. Anything else (and a renamed executable) answers ""
func sniffType(data []byte) string {
	ct := http.DetectContentType(data)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	if ct != "application/zip" {
		if _, ok := attachmentTypes[ct]; ok {
			return ct
		}
		return ""
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ""
	}
	hasTypes, docx, xlsx := false, false, false
	for _, f := range zr.File {
		switch f.Name {
		case "[Content_Types].xml":
			hasTypes = true
		case "word/document.xml":
			docx = true
		case "xl/workbook.xml":
			xlsx = true
		}
	}
	switch {
	case hasTypes && docx && !xlsx:
		return mimeDOCX
	case hasTypes && xlsx && !docx:
		return mimeXLSX
	}
	return ""
}

// validateUpload checks category, texts and files, and returns the rows to store (without object keys yet)
func validateUpload(in AttachmentUpload, spec ownerSpec) ([]domain.FileAttachment, map[string]string) {
	fields := map[string]string{}
	category := strings.TrimSpace(in.Category)
	if category == "" {
		category = "other"
	}
	if !slices.Contains(spec.categories, category) {
		fields["category"] = "Elige una categoría válida."
	}
	title, desc := strings.TrimSpace(in.Title), strings.TrimSpace(in.Description)
	if utf8.RuneCountInString(title) > maxTitleRunes {
		fields["title"] = fmt.Sprintf("El título puede tener hasta %d caracteres.", maxTitleRunes)
	}
	if utf8.RuneCountInString(desc) > maxDescRunes {
		fields["description"] = fmt.Sprintf("La descripción puede tener hasta %d caracteres.", maxDescRunes)
	}
	switch {
	case len(in.Files) == 0:
		fields["files"] = "Elige al menos un archivo."
	case len(in.Files) > maxFilesPerUpload:
		fields["files"] = fmt.Sprintf("Sube como máximo %d archivos a la vez.", maxFilesPerUpload)
	}
	var out []domain.FileAttachment
	var total int64
	if fields["files"] == "" {
		for _, f := range in.Files {
			name := cleanFileName(f.FileName)
			size := int64(len(f.Data))
			if size == 0 {
				fields["files"] = fmt.Sprintf("El archivo %s está vacío.", name)
				break
			}
			if size > maxFileAttachmentBytes {
				fields["files"] = fmt.Sprintf("%s supera los %d MB.", name, maxFileAttachmentBytes>>20)
				break
			}
			if total += size; total > maxUploadBytes {
				fields["files"] = fmt.Sprintf("Los archivos suman más de %d MB. Súbelos en partes.", maxUploadBytes>>20)
				break
			}
			ct := sniffType(f.Data)
			if ct == "" {
				fields["files"] = fmt.Sprintf("%s no es un PDF, una imagen (PNG, JPG, WebP), un Word (.docx) ni un Excel (.xlsx).", name)
				break
			}
			out = append(out, domain.FileAttachment{OwnerKind: in.OwnerKind, OwnerID: in.OwnerID, Category: category, Title: title, Description: desc, FileName: name, ContentType: ct, SizeBytes: size})
		}
	}
	if len(fields) > 0 {
		return nil, fields
	}
	return out, nil
}

// Upload stores new files of one owner: checks the role, the files (type sniffed from the bytes) and the per-owner
// limit, uploads the bytes and then the rows; on failure it removes what it already stored
func (s *AttachmentService) Upload(ctx context.Context, token string, in AttachmentUpload) ([]domain.FileAttachment, error) {
	sc, err := s.scope(ctx, token, in.InstitutionSlug)
	if err != nil {
		return nil, err
	}
	spec, owner, err := s.owner(ctx, sc.institutionID, in.OwnerKind, in.OwnerID)
	if err != nil {
		return nil, err
	}
	if !canReadOwner(sc.role, spec, owner, sc.children) {
		return nil, domain.ErrAttachmentForbidden
	}
	if !canWriteOwner(sc.role, spec, owner, sc.children) {
		return nil, domain.ErrAttachmentReadOnly
	}
	rows, fields := validateUpload(in, spec)
	if len(fields) > 0 {
		return nil, domain.Validation(fields)
	}
	// Cheap check before sending bytes to storage; the repository checks again under a lock
	existing, err := s.repo.List(ctx, sc.institutionID, in.OwnerKind, in.OwnerID)
	if err != nil {
		return nil, err
	}
	if len(existing)+len(rows) > maxFilesPerOwner {
		return nil, domain.ErrAttachmentsFull
	}
	if !s.store.Ready() {
		return nil, domain.ErrStorageUnavailable
	}
	var stored []string
	for i := range rows {
		r := &rows[i]
		r.InstitutionID, r.UploadedBy, r.UploadedName = sc.institutionID, sc.userID, sc.userName
		r.ObjectKey = fmt.Sprintf("attachments/%s/%s/%s%s", sc.institutionID, in.OwnerKind, randomHex(16), attachmentTypes[r.ContentType].ext)
		if err := s.store.Put(ctx, r.ObjectKey, r.ContentType, bytes.NewReader(in.Files[i].Data), r.SizeBytes); err != nil {
			s.removeObjects(ctx, stored)
			s.logger.Error().Err(err).Msg("attachment upload failed")
			return nil, domain.ErrStorageUnavailable
		}
		stored = append(stored, r.ObjectKey)
	}
	created, err := s.repo.Create(ctx, rows, maxFilesPerOwner)
	if err != nil {
		s.removeObjects(ctx, stored)
		return nil, err
	}
	return created, nil
}

// removeObjects deletes stored files whose rows are gone; a failure only leaves an orphan, logged
func (s *AttachmentService) removeObjects(ctx context.Context, keys []string) {
	for _, key := range keys {
		if err := s.store.Delete(context.WithoutCancel(ctx), key); err != nil {
			s.logger.Warn().Err(err).Str("key", key).Msg("orphan attachment not removed")
		}
	}
}

// Delete removes one file (row and object) when the person may change its owner
func (s *AttachmentService) Delete(ctx context.Context, token, institutionSlug, id string) error {
	sc, err := s.scope(ctx, token, institutionSlug)
	if err != nil {
		return err
	}
	f, err := s.repo.ByID(ctx, id)
	if err != nil {
		return err
	}
	if f.InstitutionID != sc.institutionID {
		return domain.ErrNotFound
	}
	spec, owner, err := s.owner(ctx, sc.institutionID, f.OwnerKind, f.OwnerID)
	if err != nil {
		return err
	}
	if !canReadOwner(sc.role, spec, owner, sc.children) {
		return domain.ErrNotFound
	}
	if !canDeleteFile(sc.role, sc.userID, spec, owner, sc.children, f) {
		return domain.ErrAttachmentReadOnly
	}
	if err := s.repo.Delete(ctx, f.ID); err != nil {
		return err
	}
	s.removeObjects(ctx, []string{f.ObjectKey})
	return nil
}

// StoredFile is an attachment ready to stream; Inline says whether the browser may open it in a tab (PDF and
// images) or must download it (Word and Excel)
type StoredFile struct {
	Body        io.ReadCloser
	Size        int64
	ContentType string
	FileName    string
	Inline      bool
}

// Open streams a file to anyone holding, in the file's institution, a role that may read its owner (the platform
// superadmin always). Everyone else gets not found, so nothing tells an outsider the file exists
func (s *AttachmentService) Open(ctx context.Context, token, id string) (StoredFile, error) {
	me, err := s.auth.Me(ctx, token)
	if err != nil {
		return StoredFile{}, err
	}
	f, err := s.repo.ByID(ctx, id)
	if err != nil {
		return StoredFile{}, err
	}
	spec, ok := ownerSpecs[f.OwnerKind]
	if !ok {
		return StoredFile{}, domain.ErrNotFound
	}
	owner, err := s.repo.Owner(ctx, f.OwnerKind, f.OwnerID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return StoredFile{}, err
	}
	var children []string
	permitted := false
	for _, r := range me.Roles {
		if r.RoleCode == domain.RoleSuperadmin {
			permitted = true
			break
		}
		if r.Institution == nil || r.Institution.ID != f.InstitutionID {
			continue
		}
		if isFamily(r.RoleCode) && children == nil {
			if children, err = s.familyStudents(ctx, f.InstitutionID, me.User.Email); err != nil {
				return StoredFile{}, err
			}
		}
		if canReadOwner(r.RoleCode, spec, owner, children) {
			permitted = true
			break
		}
	}
	if !permitted {
		return StoredFile{}, domain.ErrNotFound
	}
	body, size, err := s.store.Get(ctx, f.ObjectKey)
	if err != nil {
		s.logger.Error().Err(err).Str("key", f.ObjectKey).Msg("attachment read failed")
		return StoredFile{}, domain.ErrStorageUnavailable
	}
	return StoredFile{Body: body, Size: size, ContentType: f.ContentType, FileName: f.FileName, Inline: attachmentTypes[f.ContentType].inline}, nil
}
