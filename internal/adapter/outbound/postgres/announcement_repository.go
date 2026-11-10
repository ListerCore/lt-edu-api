package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// AnnouncementRepository implements port.AnnouncementRepository over the comms schema
type AnnouncementRepository struct {
	c *Client
}

func NewAnnouncementRepository(c *Client) *AnnouncementRepository {
	return &AnnouncementRepository{c: c}
}

var _ port.AnnouncementRepository = (*AnnouncementRepository)(nil)

func (r *AnnouncementRepository) InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error) {
	var i domain.Institution
	err := r.c.pool.QueryRow(ctx, `SELECT id, slug, name, kind FROM iam.institutions WHERE slug = $1 AND is_active`, slug).
		Scan(&i.ID, &i.Slug, &i.Name, &i.Kind)
	return i, notFound(err)
}

const announcementColumns = `a.id, a.institution_id, i.slug, a.slug, a.kind, a.audience, a.title, a.body, a.is_public, a.pinned,
	coalesce(a.author_id::text, ''), coalesce(u.full_name, ''), a.published_at, a.updated_at, a.featured_from, a.featured_until,
	coalesce(a.cta_label, ''), coalesce(a.cta_url, '')`

const announcementFrom = ` FROM comms.announcements a
	JOIN iam.institutions i ON i.id = a.institution_id
	LEFT JOIN iam.users u ON u.id = a.author_id`

func scanAnnouncement(row pgx.Row) (domain.Announcement, error) {
	var a domain.Announcement
	err := row.Scan(&a.ID, &a.InstitutionID, &a.InstitutionSlug, &a.Slug, &a.Kind, &a.Audience, &a.Title, &a.Body,
		&a.IsPublic, &a.Pinned, &a.AuthorID, &a.AuthorName, &a.PublishedAt, &a.UpdatedAt, &a.FeaturedFrom, &a.FeaturedUntil,
		&a.CTALabel, &a.CTAURL)
	return a, err
}

func (r *AnnouncementRepository) List(ctx context.Context, f domain.AnnouncementFilter) ([]domain.Announcement, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := r.c.pool.Query(ctx, `SELECT `+announcementColumns+announcementFrom+`
		WHERE a.institution_id = $1
		  AND ($2::text[] IS NULL OR cardinality($2::text[]) = 0 OR a.audience = ANY($2))
		  AND (NOT $3 OR a.is_public)
		ORDER BY a.pinned DESC, a.published_at DESC, a.id DESC
		LIMIT $4`, f.InstitutionID, f.Audiences, f.OnlyPublic, limit)
	if err != nil {
		return nil, err
	}
	return r.collect(ctx, rows)
}

func (r *AnnouncementRepository) Featured(ctx context.Context, institutionID string) ([]domain.Announcement, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+announcementColumns+announcementFrom+`
		WHERE a.institution_id = $1 AND a.is_public AND a.featured_from <= now() AND a.featured_until > now()
		ORDER BY a.published_at DESC, a.id DESC
		LIMIT 20`, institutionID)
	if err != nil {
		return nil, err
	}
	return r.collect(ctx, rows)
}

// collect scans a feed and loads the attachments of every row in one query
func (r *AnnouncementRepository) collect(ctx context.Context, rows pgx.Rows) ([]domain.Announcement, error) {
	var out []domain.Announcement
	index := map[string]int{}
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		index[a.ID] = len(out)
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}

	ids := make([]string, len(out))
	for i, a := range out {
		ids[i] = a.ID
	}
	attachments, err := r.attachments(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, list := range attachments {
		out[index[id]].Attachments = list
	}
	return out, nil
}

func (r *AnnouncementRepository) attachments(ctx context.Context, announcementIDs []string) (map[string][]domain.Attachment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT announcement_id, id, kind, object_key, file_name, content_type, size_bytes, position
		FROM comms.announcement_attachments WHERE announcement_id = ANY($1::uuid[]) ORDER BY announcement_id, position`, announcementIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]domain.Attachment{}
	for rows.Next() {
		var announcementID string
		var a domain.Attachment
		if err := rows.Scan(&announcementID, &a.ID, &a.Kind, &a.ObjectKey, &a.FileName, &a.ContentType, &a.SizeBytes, &a.Position); err != nil {
			return nil, err
		}
		out[announcementID] = append(out[announcementID], a)
	}
	return out, rows.Err()
}

func (r *AnnouncementRepository) one(ctx context.Context, where string, arg any) (domain.Announcement, error) {
	a, err := scanAnnouncement(r.c.pool.QueryRow(ctx, `SELECT `+announcementColumns+announcementFrom+` WHERE `+where, arg))
	if err != nil {
		return a, notFound(err)
	}
	attachments, err := r.attachments(ctx, []string{a.ID})
	if err != nil {
		return a, err
	}
	a.Attachments = attachments[a.ID]
	return a, nil
}

func (r *AnnouncementRepository) BySlug(ctx context.Context, slug string) (domain.Announcement, error) {
	return r.one(ctx, `a.slug = $1`, slug)
}

func (r *AnnouncementRepository) ByID(ctx context.Context, id string) (domain.Announcement, error) {
	return r.one(ctx, `a.id = $1`, id)
}

func (r *AnnouncementRepository) Create(ctx context.Context, a domain.Announcement, authorID string) (domain.Announcement, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return a, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var author *string
	if authorID != "" {
		author = &authorID
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO comms.announcements (institution_id, slug, kind, audience, title, body, is_public, author_id,
			featured_from, featured_until, cta_label, cta_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, nullif($11, ''), nullif($12, '')) RETURNING id, published_at`,
		a.InstitutionID, a.Slug, a.Kind, a.Audience, a.Title, a.Body, a.IsPublic, author,
		a.FeaturedFrom, a.FeaturedUntil, a.CTALabel, a.CTAURL).Scan(&a.ID, &a.PublishedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "announcements_slug_key" {
		return a, domain.ErrSlugTaken
	}
	if err != nil {
		return a, err
	}
	for i := range a.Attachments {
		att := &a.Attachments[i]
		if err := tx.QueryRow(ctx, `
			INSERT INTO comms.announcement_attachments (announcement_id, kind, object_key, file_name, content_type, size_bytes, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			a.ID, att.Kind, att.ObjectKey, att.FileName, att.ContentType, att.SizeBytes, att.Position).Scan(&att.ID); err != nil {
			return a, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return a, err
	}
	return r.ByID(ctx, a.ID)
}

func (r *AnnouncementRepository) Update(ctx context.Context, a domain.Announcement, keep []string, added []domain.Attachment) (domain.Announcement, []string, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return a, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE comms.announcements SET kind = $2, audience = $3, title = $4, body = $5, is_public = $6,
			featured_from = $7, featured_until = $8, cta_label = nullif($9, ''), cta_url = nullif($10, '')
		WHERE id = $1`,
		a.ID, a.Kind, a.Audience, a.Title, a.Body, a.IsPublic, a.FeaturedFrom, a.FeaturedUntil, a.CTALabel, a.CTAURL)
	if err != nil {
		return a, nil, err
	}
	if tag.RowsAffected() == 0 {
		return a, nil, domain.ErrNotFound
	}

	// Dropped attachments go first; their objects are removed by the caller once this commits
	var removed []string
	rows, err := tx.Query(ctx, `DELETE FROM comms.announcement_attachments
		WHERE announcement_id = $1 AND NOT (id = ANY($2::uuid[])) RETURNING object_key`, a.ID, keep)
	if err != nil {
		return a, nil, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return a, nil, err
		}
		removed = append(removed, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return a, nil, err
	}

	// Positions may collide while they are rewritten; the unique check runs at commit
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS comms.announcement_attachments_position_key DEFERRED`); err != nil {
		return a, nil, err
	}
	for position, id := range keep {
		tag, err := tx.Exec(ctx, `UPDATE comms.announcement_attachments SET position = $3 WHERE announcement_id = $1 AND id = $2`, a.ID, id, position)
		if err != nil {
			return a, nil, err
		}
		if tag.RowsAffected() == 0 {
			return a, nil, domain.ErrNotFound
		}
	}
	for _, att := range added {
		if _, err := tx.Exec(ctx, `
			INSERT INTO comms.announcement_attachments (announcement_id, kind, object_key, file_name, content_type, size_bytes, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			a.ID, att.Kind, att.ObjectKey, att.FileName, att.ContentType, att.SizeBytes, att.Position); err != nil {
			return a, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return a, nil, err
	}
	updated, err := r.ByID(ctx, a.ID)
	return updated, removed, err
}

func (r *AnnouncementRepository) Delete(ctx context.Context, id string) ([]string, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT object_key FROM comms.announcement_attachments WHERE announcement_id = $1`, id)
	if err != nil {
		return nil, err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	rows.Close()
	tag, err := tx.Exec(ctx, `DELETE FROM comms.announcements WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, domain.ErrNotFound
	}
	return keys, tx.Commit(ctx)
}
