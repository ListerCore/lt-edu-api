package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// AttachmentRepository implements port.AttachmentRepository over the attachments schema
type AttachmentRepository struct {
	c *Client
}

func NewAttachmentRepository(c *Client) *AttachmentRepository {
	return &AttachmentRepository{c: c}
}

var _ port.AttachmentRepository = (*AttachmentRepository)(nil)

func (r *AttachmentRepository) InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error) {
	var i domain.Institution
	err := r.c.pool.QueryRow(ctx, `SELECT id, slug, name, kind FROM iam.institutions WHERE slug = $1 AND is_active`, slug).
		Scan(&i.ID, &i.Slug, &i.Name, &i.Kind)
	return i, notFound(err)
}

// ownerQueries reads (institution, student) of each owner kind that has a table. Kinds missing here
// (progress_report, inclusion_support) are only checked for their id format; apafa_meeting is in draftOwnerQueries
var ownerQueries = map[string]string{
	domain.OwnerEnrollment:     `SELECT institution_id, student_id::text FROM enrollment.enrollments WHERE id = $1`,
	domain.OwnerStudent:        `SELECT institution_id, id::text FROM people.students WHERE id = $1`,
	domain.OwnerStaff:          `SELECT institution_id, '' FROM people.staff WHERE id = $1`,
	domain.OwnerTutoring:       `SELECT institution_id, student_id::text FROM wellbeing.interviews WHERE id = $1`,
	domain.OwnerDiscipline:     `SELECT institution_id, student_id::text FROM wellbeing.incidents WHERE id = $1`,
	domain.OwnerPsychology:     `SELECT institution_id, student_id::text FROM wellbeing.psych_cases WHERE id = $1`,
	domain.OwnerIndividualPlan: `SELECT institution_id, student_id::text FROM wellbeing.individual_plans WHERE id = $1`,
	domain.OwnerTherapy:        `SELECT institution_id, student_id::text FROM wellbeing.therapy_sessions WHERE id = $1`,
	domain.OwnerPeriod:         `SELECT institution_id, '' FROM academic.periods WHERE id = $1`,
	domain.OwnerClosing:        `SELECT institution_id, '' FROM assessment.records WHERE id = $1`,
	domain.OwnerInternship:     `SELECT institution_id, student_id::text FROM programs.internships WHERE id = $1`,
	// The titulación dossier of a student (thesis, acta de sustentación, receipts): it fills up while the
	// requirements are verified, before the degree certificate exists, so it hangs on the student
	domain.OwnerDegree:                  `SELECT institution_id, id::text FROM people.students WHERE id = $1`,
	domain.OwnerAttendanceJustification: `SELECT institution_id, student_id::text FROM attendance.justifications WHERE id = $1`,
	domain.OwnerAuthorization:           `SELECT institution_id, '' FROM academic.authorizations WHERE id = $1`,
	// The platform sheet is one per institution, so the owner id is the institution's own
	domain.OwnerPlatformSheet: `SELECT id, '' FROM iam.institutions WHERE id = $1`,
	domain.OwnerLibraryItem:   `SELECT institution_id, '' FROM classroom.library_items WHERE id = $1`,
}

// draftOwnerQueries read (institution, student, hidden from families) of owners that may still be drafts: an APAFA
// meeting not convened yet is invisible to guardians, and so are its files
var draftOwnerQueries = map[string]string{
	domain.OwnerApafaMeeting: `SELECT institution_id, '', notified_at IS NULL FROM apafa.meetings WHERE id = $1`,
	// Material is hidden from families until the item, its unit and its room are all published
	domain.OwnerClassroomItem: `SELECT r.institution_id, '', NOT (i.published AND u.published AND r.status = 'published')
		FROM classroom.items i JOIN classroom.units u ON u.id = i.unit_id JOIN classroom.rooms r ON r.id = i.room_id WHERE i.id = $1`,
}

func (r *AttachmentRepository) Owner(ctx context.Context, ownerKind, ownerID string) (domain.AttachmentOwner, error) {
	if q, ok := draftOwnerQueries[ownerKind]; ok {
		o := domain.AttachmentOwner{Checked: true}
		err := r.c.pool.QueryRow(ctx, q, ownerID).Scan(&o.InstitutionID, &o.StudentID, &o.FamilyHidden)
		return o, academicErr(err)
	}
	q, ok := ownerQueries[ownerKind]
	if !ok {
		return domain.AttachmentOwner{}, nil
	}
	o := domain.AttachmentOwner{Checked: true}
	err := r.c.pool.QueryRow(ctx, q, ownerID).Scan(&o.InstitutionID, &o.StudentID)
	return o, academicErr(err)
}

func (r *AttachmentRepository) StudentsForEmail(ctx context.Context, institutionID, email string) ([]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id::text FROM people.students s
		WHERE s.institution_id = $1 AND s.status = 'active' AND (lower(s.email) = lower($2) OR EXISTS (
			SELECT 1 FROM people.student_guardians sg JOIN people.guardians gu ON gu.id = sg.guardian_id WHERE sg.student_id = s.id AND lower(gu.email) = lower($2)))`,
		institutionID, email)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if ids == nil {
		ids = []string{}
	}
	return ids, err
}

const attachmentColumns = `id, institution_id, owner_kind, owner_id, category, coalesce(title, ''), coalesce(description, ''), object_key,
	file_name, content_type, size_bytes, coalesce(uploaded_by::text, ''), uploaded_name, created_at`

func scanAttachment(row pgx.Row) (domain.FileAttachment, error) {
	var f domain.FileAttachment
	err := row.Scan(&f.ID, &f.InstitutionID, &f.OwnerKind, &f.OwnerID, &f.Category, &f.Title, &f.Description, &f.ObjectKey,
		&f.FileName, &f.ContentType, &f.SizeBytes, &f.UploadedBy, &f.UploadedName, &f.CreatedAt)
	return f, err
}

func (r *AttachmentRepository) List(ctx context.Context, institutionID, ownerKind, ownerID string) ([]domain.FileAttachment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+attachmentColumns+` FROM attachments.files
		WHERE institution_id = $1 AND owner_kind = $2 AND owner_id = $3 ORDER BY created_at DESC, id DESC`, institutionID, ownerKind, ownerID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.FileAttachment, error) { return scanAttachment(row) })
	if out == nil {
		out = []domain.FileAttachment{}
	}
	return out, err
}

// optionalText stores "" as NULL (optional title, description and uploader)
func optionalText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r *AttachmentRepository) Create(ctx context.Context, files []domain.FileAttachment, max int) ([]domain.FileAttachment, error) {
	if len(files) == 0 {
		return []domain.FileAttachment{}, nil
	}
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	first := files[0]
	// One lock per owner: concurrent uploads to the same record count one after the other
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('attachments:' || $1 || ':' || $2, 0))`, first.OwnerKind, first.OwnerID); err != nil {
		return nil, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments.files WHERE institution_id = $1 AND owner_kind = $2 AND owner_id = $3`,
		first.InstitutionID, first.OwnerKind, first.OwnerID).Scan(&count); err != nil {
		return nil, err
	}
	if count+len(files) > max {
		return nil, domain.ErrAttachmentsFull
	}
	out := make([]domain.FileAttachment, 0, len(files))
	for _, f := range files {
		created, err := scanAttachment(tx.QueryRow(ctx, `INSERT INTO attachments.files
			(institution_id, owner_kind, owner_id, category, title, description, object_key, file_name, content_type, size_bytes, uploaded_by, uploaded_name)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING `+attachmentColumns,
			f.InstitutionID, f.OwnerKind, f.OwnerID, f.Category, optionalText(f.Title), optionalText(f.Description), f.ObjectKey, f.FileName,
			f.ContentType, f.SizeBytes, optionalText(f.UploadedBy), f.UploadedName))
		if err != nil {
			return nil, err
		}
		out = append(out, created)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *AttachmentRepository) ByID(ctx context.Context, id string) (domain.FileAttachment, error) {
	f, err := scanAttachment(r.c.pool.QueryRow(ctx, `SELECT `+attachmentColumns+` FROM attachments.files WHERE id = $1`, id))
	return f, academicErr(err)
}

func (r *AttachmentRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM attachments.files WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
