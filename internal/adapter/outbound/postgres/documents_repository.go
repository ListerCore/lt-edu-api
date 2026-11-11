package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// DocumentRepository implements port.DocumentRepository on the documents schema
type DocumentRepository struct{ c *Client }

func NewDocumentRepository(c *Client) *DocumentRepository { return &DocumentRepository{c: c} }

var _ port.DocumentRepository = (*DocumentRepository)(nil)

// Signers of a kind in their order, with the staff member's current name
func (r *DocumentRepository) Signers(ctx context.Context, institutionID, kind string) ([]domain.DocumentSigner, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id, s.staff_id, st.given_names || ' ' || st.surnames, s.position, s.sort
		FROM documents.signers s JOIN people.staff st ON st.id = s.staff_id
		WHERE s.institution_id = $1 AND s.kind = $2 ORDER BY s.sort`, institutionID, kind)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.DocumentSigner, error) {
		var s domain.DocumentSigner
		return s, row.Scan(&s.ID, &s.StaffID, &s.Name, &s.Position, &s.Sort)
	})
}

// SetSigners rewrites the list of a kind; staff of another institution are refused by the join
func (r *DocumentRepository) SetSigners(ctx context.Context, institutionID, kind string, signers []domain.DocumentSigner) error {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM documents.signers WHERE institution_id = $1 AND kind = $2`, institutionID, kind); err != nil {
		return academicErr(err)
	}
	for i, s := range signers {
		tag, err := tx.Exec(ctx, `INSERT INTO documents.signers (institution_id, kind, staff_id, position, sort)
			SELECT $1, $2, id, $4, $5 FROM people.staff WHERE institution_id = $1 AND id = $3`, institutionID, kind, s.StaffID, s.Position, i+1)
		if err != nil {
			return academicErr(err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
	}
	return tx.Commit(ctx)
}

// Candidates are the active staff, by surname
func (r *DocumentRepository) Candidates(ctx context.Context, institutionID string) ([]port.SignerCandidate, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, given_names || ' ' || surnames, position FROM people.staff
		WHERE institution_id = $1 AND status = 'active' ORDER BY surnames, given_names`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (port.SignerCandidate, error) {
		var c port.SignerCandidate
		return c, row.Scan(&c.StaffID, &c.Name, &c.Position)
	})
}

const documentColumns = `d.id, d.institution_id, i.name, d.kind, d.scope_key, d.title, d.tracking_code, d.created_at
	FROM documents.issued d JOIN iam.institutions i ON i.id = d.institution_id`

func scanDocument(row pgx.Row) (domain.IssuedDocument, error) {
	var d domain.IssuedDocument
	err := row.Scan(&d.ID, &d.InstitutionID, &d.Institution, &d.Kind, &d.ScopeKey, &d.Title, &d.TrackingCode, &d.CreatedAt)
	return d, academicErr(err)
}

func (r *DocumentRepository) Documents(ctx context.Context, institutionID, kind, prefix string) ([]domain.IssuedDocument, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+documentColumns+` WHERE d.institution_id = $1 AND d.kind = $2 AND starts_with(d.scope_key, $3)
		ORDER BY d.title`, institutionID, kind, prefix)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.IssuedDocument, error) { return scanDocument(row) })
}

func (r *DocumentRepository) DocumentByTracking(ctx context.Context, tracking string) (domain.IssuedDocument, error) {
	return scanDocument(r.c.pool.QueryRow(ctx, `SELECT `+documentColumns+` WHERE d.tracking_code = $1`, tracking))
}

const versionColumns = `v.id, v.document_id, v.number, v.verification_code, v.content_hash, v.status, v.note, v.created_name, v.created_at, v.published_at`

func scanVersion(row pgx.Row, extra ...any) (domain.DocumentVersion, error) {
	var v domain.DocumentVersion
	dest := append([]any{&v.ID, &v.DocumentID, &v.Number, &v.VerificationCode, &v.ContentHash, &v.Status, &v.Note, &v.CreatedName, &v.CreatedAt, &v.PublishedAt}, extra...)
	return v, row.Scan(dest...)
}

// signatures loads the signatures of some versions
func (r *DocumentRepository) signatures(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, versions []domain.DocumentVersion) error {
	if len(versions) == 0 {
		return nil
	}
	ids := make([]string, len(versions))
	at := map[string]int{}
	for i, v := range versions {
		ids[i], at[v.ID] = v.ID, i
	}
	rows, err := q.Query(ctx, `SELECT version_id, sort, coalesce(staff_id::text, ''), name, position, signed_at, coalesce(code, '')
		FROM documents.signatures WHERE version_id = ANY($1::uuid[]) ORDER BY version_id, sort`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var s domain.DocumentSignature
		if err := rows.Scan(&id, &s.Sort, &s.StaffID, &s.Name, &s.Position, &s.SignedAt, &s.Code); err != nil {
			return err
		}
		versions[at[id]].Signatures = append(versions[at[id]].Signatures, s)
	}
	return rows.Err()
}

func (r *DocumentRepository) Versions(ctx context.Context, documentID string) ([]domain.DocumentVersion, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+versionColumns+` FROM documents.versions v WHERE v.document_id = $1 ORDER BY v.number DESC`, documentID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.DocumentVersion, error) { return scanVersion(row) })
	if err != nil {
		return nil, err
	}
	return out, r.signatures(ctx, r.c.pool, out)
}

func (r *DocumentRepository) Version(ctx context.Context, institutionID, id string) (domain.IssuedDocument, domain.DocumentVersion, error) {
	var content []byte
	v, err := scanVersion(r.c.pool.QueryRow(ctx, `SELECT `+versionColumns+`, v.content FROM documents.versions v
		JOIN documents.issued d ON d.id = v.document_id WHERE d.institution_id = $1 AND v.id = $2`, institutionID, id), &content)
	if err != nil {
		return domain.IssuedDocument{}, domain.DocumentVersion{}, academicErr(err)
	}
	v.Content = content
	d, err := scanDocument(r.c.pool.QueryRow(ctx, `SELECT `+documentColumns+` WHERE d.id = $1`, v.DocumentID))
	if err != nil {
		return d, v, err
	}
	list := []domain.DocumentVersion{v}
	err = r.signatures(ctx, r.c.pool, list)
	return d, list[0], err
}

func (r *DocumentRepository) VersionByNumber(ctx context.Context, documentID string, number int) (domain.DocumentVersion, error) {
	var content []byte
	v, err := scanVersion(r.c.pool.QueryRow(ctx, `SELECT `+versionColumns+`, v.content FROM documents.versions v
		WHERE v.document_id = $1 AND v.number = $2`, documentID, number), &content)
	if err != nil {
		return v, academicErr(err)
	}
	v.Content = content
	list := []domain.DocumentVersion{v}
	err = r.signatures(ctx, r.c.pool, list)
	return list[0], err
}

func (r *DocumentRepository) Events(ctx context.Context, documentID string) ([]domain.DocumentEvent, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT version_number, action, actor_name, note, at FROM documents.events WHERE document_id = $1 ORDER BY at, id`, documentID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.DocumentEvent, error) {
		var e domain.DocumentEvent
		return e, row.Scan(&e.Version, &e.Action, &e.Actor, &e.Note, &e.At)
	})
}

func (r *DocumentRepository) CreateVersion(ctx context.Context, doc domain.IssuedDocument, v domain.DocumentVersion, userID string) (domain.IssuedDocument, domain.DocumentVersion, bool, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return doc, v, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The update on conflict keeps the title current and locks the row, so two generations queue up
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO documents.issued (institution_id, kind, scope_key, title, tracking_code) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (institution_id, kind, scope_key) DO UPDATE SET title = EXCLUDED.title RETURNING id`,
		doc.InstitutionID, doc.Kind, doc.ScopeKey, doc.Title, doc.TrackingCode).Scan(&id); err != nil {
		return doc, v, false, academicErr(err)
	}
	d, err := scanDocument(tx.QueryRow(ctx, `SELECT `+documentColumns+` WHERE d.id = $1`, id))
	if err != nil {
		return doc, v, false, err
	}
	// Same content as the newest live version: nothing to issue
	var sameID string
	err = tx.QueryRow(ctx, `SELECT id FROM documents.versions WHERE document_id = $1 AND status IN ('pending', 'published')
		ORDER BY number DESC LIMIT 1`, id).Scan(&sameID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return d, v, false, err
	}
	if sameID != "" {
		var hash string
		if err := tx.QueryRow(ctx, `SELECT content_hash FROM documents.versions WHERE id = $1`, sameID).Scan(&hash); err != nil {
			return d, v, false, err
		}
		if hash == v.ContentHash {
			if err := tx.Commit(ctx); err != nil {
				return d, v, false, err
			}
			_, same, err := r.Version(ctx, d.InstitutionID, sameID)
			return d, same, false, err
		}
	}
	var cancelled []int
	rows, err := tx.Query(ctx, `UPDATE documents.versions SET status = 'cancelled' WHERE document_id = $1 AND status = 'pending' RETURNING number`, id)
	if err != nil {
		return d, v, false, err
	}
	if cancelled, err = pgx.CollectRows(rows, pgx.RowTo[int]); err != nil {
		return d, v, false, err
	}
	var vid string
	if err := tx.QueryRow(ctx, `INSERT INTO documents.versions (document_id, number, verification_code, content, content_hash, note, created_by, created_name)
		SELECT $1, coalesce(max(number), 0) + 1, $2, $3, $4, $5, nullif($6, '')::uuid, $7 FROM documents.versions WHERE document_id = $1
		RETURNING id`, id, v.VerificationCode, v.Content, v.ContentHash, v.Note, userID, v.CreatedName).Scan(&vid); err != nil {
		return d, v, false, academicErr(err)
	}
	for _, s := range v.Signatures {
		if _, err := tx.Exec(ctx, `INSERT INTO documents.signatures (version_id, sort, staff_id, name, position) VALUES ($1, $2, nullif($3, '')::uuid, $4, $5)`,
			vid, s.Sort, s.StaffID, s.Name, s.Position); err != nil {
			return d, v, false, academicErr(err)
		}
	}
	var number int
	if err := tx.QueryRow(ctx, `SELECT number FROM documents.versions WHERE id = $1`, vid).Scan(&number); err != nil {
		return d, v, false, err
	}
	for _, n := range cancelled {
		if _, err := tx.Exec(ctx, `INSERT INTO documents.events (document_id, version_number, action, actor_name, note) VALUES ($1, $2, 'cancelled', $3, $4)`,
			id, n, v.CreatedName, "Reemplazada por la versión "+strconv.Itoa(number)+" antes de firmarse"); err != nil {
			return d, v, false, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO documents.events (document_id, version_number, action, actor_name, note) VALUES ($1, $2, 'generated', $3, $4)`,
		id, number, v.CreatedName, v.Note); err != nil {
		return d, v, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return d, v, false, err
	}
	_, created, err := r.Version(ctx, d.InstitutionID, vid)
	return d, created, true, err
}

func (r *DocumentRepository) Sign(ctx context.Context, versionID string, sort int, userID, code string, at time.Time) (bool, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var documentID, status string
	var number int
	if err := tx.QueryRow(ctx, `SELECT document_id, status, number FROM documents.versions WHERE id = $1 FOR UPDATE`, versionID).Scan(&documentID, &status, &number); err != nil {
		return false, academicErr(err)
	}
	if status != domain.VersionPending {
		return false, domain.ErrVersionClosed
	}
	var name string
	if err := tx.QueryRow(ctx, `UPDATE documents.signatures SET signed_at = $3, signed_by = nullif($4, '')::uuid, code = $5
		WHERE version_id = $1 AND sort = $2 AND signed_at IS NULL RETURNING name`, versionID, sort, at, userID, code).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, domain.ErrNotSigner
		}
		return false, academicErr(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO documents.events (document_id, version_number, action, actor_name, at) VALUES ($1, $2, 'signed', $3, $4)`,
		documentID, number, name, at); err != nil {
		return false, err
	}
	var missing int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM documents.signatures WHERE version_id = $1 AND signed_at IS NULL`, versionID).Scan(&missing); err != nil {
		return false, err
	}
	if missing > 0 {
		return false, tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `UPDATE documents.versions SET status = 'superseded' WHERE document_id = $1 AND status = 'published' RETURNING number`, documentID)
	if err != nil {
		return false, err
	}
	old, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE documents.versions SET status = 'published', published_at = $2 WHERE id = $1`, versionID, at); err != nil {
		return false, err
	}
	for _, n := range old {
		if _, err := tx.Exec(ctx, `INSERT INTO documents.events (document_id, version_number, action, note, at) VALUES ($1, $2, 'superseded', $3, $4)`,
			documentID, n, "Reemplazada por la versión "+strconv.Itoa(number), at); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO documents.events (document_id, version_number, action, at) VALUES ($1, $2, 'published', $3)`, documentID, number, at); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
