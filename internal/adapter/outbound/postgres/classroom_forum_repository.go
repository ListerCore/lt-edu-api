package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
)

// Forums of the virtual classroom: classroom.forums, posts and forum_reads

func (r *ClassroomRepository) EnsureForum(ctx context.Context, itemID string) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO classroom.forums (item_id) VALUES ($1) ON CONFLICT DO NOTHING`, itemID)
	return academicErr(err)
}

func (r *ClassroomRepository) Forum(ctx context.Context, institutionID, itemID string) (domain.Forum, error) {
	var f domain.Forum
	err := r.c.pool.QueryRow(ctx, `SELECT i.id, i.room_id, i.unit_id, i.title, i.body, i.published, f.kind, f.closed, f.closed_by_name
		FROM classroom.forums f JOIN classroom.items i ON i.id = f.item_id JOIN classroom.rooms r ON r.id = i.room_id
		WHERE r.institution_id = $1 AND f.item_id = $2`, institutionID, itemID).
		Scan(&f.ItemID, &f.RoomID, &f.UnitID, &f.Title, &f.Prompt, &f.Published, &f.Kind, &f.Closed, &f.ClosedByName)
	return f, academicErr(err)
}

func (r *ClassroomRepository) SaveForum(ctx context.Context, institutionID string, f domain.Forum) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE classroom.forums fo SET kind = $3, closed = $4, closed_by_name = $5
		FROM classroom.items i, classroom.rooms r WHERE i.id = fo.item_id AND r.id = i.room_id AND r.institution_id = $1 AND fo.item_id = $2`,
		institutionID, f.ItemID, f.Kind, f.Closed, f.ClosedByName)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

const postColumns = `p.id, p.item_id, coalesce(p.parent_id::text, ''), coalesce(p.author_user_id::text, ''), coalesce(p.author_student_id::text, ''),
	p.author_name, p.author_role, p.body, p.pinned, p.hidden, p.hidden_reason, p.hidden_by_name, p.created_at, p.edited_at,
	(SELECT count(*) FROM classroom.posts c WHERE c.parent_id = p.id)`

func scanPost(row pgx.Row) (domain.ForumPost, error) {
	var p domain.ForumPost
	var replies int64
	err := row.Scan(&p.ID, &p.ItemID, &p.ParentID, &p.AuthorUserID, &p.AuthorStudentID, &p.AuthorName, &p.AuthorRole, &p.Body, &p.Pinned, &p.Hidden,
		&p.HiddenReason, &p.HiddenByName, &p.CreatedAt, &p.EditedAt, &replies)
	p.Replies = int(replies)
	return p, err
}

func (r *ClassroomRepository) Posts(ctx context.Context, itemID string) ([]domain.ForumPost, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+postColumns+` FROM classroom.posts p WHERE p.item_id = $1 ORDER BY p.created_at, p.id`, itemID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ForumPost, error) { return scanPost(row) })
	if out == nil {
		out = []domain.ForumPost{}
	}
	return out, academicErr(err)
}

func (r *ClassroomRepository) Post(ctx context.Context, institutionID, id string) (domain.ForumPost, error) {
	p, err := scanPost(r.c.pool.QueryRow(ctx, `SELECT `+postColumns+` FROM classroom.posts p
		JOIN classroom.items i ON i.id = p.item_id JOIN classroom.rooms r ON r.id = i.room_id
		WHERE r.institution_id = $1 AND p.id = $2`, institutionID, id))
	return p, academicErr(err)
}

func (r *ClassroomRepository) CreatePost(ctx context.Context, p domain.ForumPost) (domain.ForumPost, error) {
	var id string
	// A reply must hang on a thread of the same forum
	err := r.c.pool.QueryRow(ctx, `INSERT INTO classroom.posts (item_id, parent_id, author_user_id, author_student_id, author_name, author_role, body)
		SELECT $1, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7
		WHERE $2::uuid IS NULL OR EXISTS (SELECT 1 FROM classroom.posts WHERE id = $2::uuid AND item_id = $1 AND parent_id IS NULL)
		RETURNING id`, p.ItemID, nilIfEmpty(p.ParentID), nilIfEmpty(p.AuthorUserID), nilIfEmpty(p.AuthorStudentID), p.AuthorName, p.AuthorRole, p.Body).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ForumPost{}, domain.ErrReplyToReply
	}
	if err != nil {
		return domain.ForumPost{}, academicErr(err)
	}
	return scanPost(r.c.pool.QueryRow(ctx, `SELECT `+postColumns+` FROM classroom.posts p WHERE p.id = $1`, id))
}

func (r *ClassroomRepository) EditPost(ctx context.Context, id, body string, at time.Time) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE classroom.posts SET body = $2, edited_at = $3 WHERE id = $1 AND NOT hidden`, id, body, at)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrPostHidden
	}
	return academicErr(err)
}

func (r *ClassroomRepository) DeletePost(ctx context.Context, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM classroom.posts p WHERE p.id = $1 AND NOT EXISTS (SELECT 1 FROM classroom.posts c WHERE c.parent_id = p.id)`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrPostHasReplies
	}
	return academicErr(err)
}

func (r *ClassroomRepository) ModeratePost(ctx context.Context, p domain.ForumPost) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE classroom.posts SET pinned = $2, hidden = $3, hidden_reason = $4, hidden_by_name = $5 WHERE id = $1`,
		p.ID, p.Pinned, p.Hidden, p.HiddenReason, p.HiddenByName)
	return academicErr(err)
}

func (r *ClassroomRepository) LastRead(ctx context.Context, itemID, userID string) (*time.Time, error) {
	var t time.Time
	err := r.c.pool.QueryRow(ctx, `SELECT read_at FROM classroom.forum_reads WHERE item_id = $1 AND user_id = $2`, itemID, userID).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, academicErr(err)
	}
	return &t, nil
}

func (r *ClassroomRepository) MarkRead(ctx context.Context, itemID, userID string, at time.Time) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO classroom.forum_reads (item_id, user_id, read_at) VALUES ($1, $2, $3)
		ON CONFLICT (item_id, user_id) DO UPDATE SET read_at = EXCLUDED.read_at`, itemID, userID, at)
	return academicErr(err)
}

func (r *ClassroomRepository) ForumParticipation(ctx context.Context, institutionID, itemID string) ([]domain.ForumParticipation, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT st.id::text,
		st.paternal_surname || CASE WHEN st.maternal_surname <> '' THEN ' ' || st.maternal_surname ELSE '' END || ', ' || st.given_names, st.code,
		(SELECT count(*) FROM classroom.posts p WHERE p.item_id = i.id AND p.author_student_id = st.id AND p.parent_id IS NULL),
		(SELECT count(*) FROM classroom.posts p WHERE p.item_id = i.id AND p.author_student_id = st.id AND p.parent_id IS NOT NULL),
		(SELECT max(created_at) FROM classroom.posts p WHERE p.item_id = i.id AND p.author_student_id = st.id)
		FROM classroom.items i
		JOIN classroom.rooms r ON r.id = i.room_id
		JOIN enrollment.enrollments e ON e.period_id = r.period_id AND e.status = 'active' AND e.grade_id IS NOT DISTINCT FROM r.grade_id
		JOIN academic.subjects sub ON sub.id = r.subject_id
		JOIN academic.curricula c ON c.id = sub.curriculum_id AND c.level_id = e.level_id
		JOIN people.students st ON st.id = e.student_id
		WHERE r.institution_id = $1 AND i.id = $2
		ORDER BY st.paternal_surname, st.maternal_surname, st.given_names`, institutionID, itemID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ForumParticipation, error) {
		var x domain.ForumParticipation
		var threads, replies int64
		err := row.Scan(&x.StudentID, &x.StudentName, &x.StudentCode, &threads, &replies, &x.LastAt)
		x.Threads, x.Replies = int(threads), int(replies)
		return x, err
	})
	if out == nil {
		out = []domain.ForumParticipation{}
	}
	return out, academicErr(err)
}
