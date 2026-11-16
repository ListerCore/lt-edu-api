package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ClassroomRepository implements port.ClassroomRepository over the classroom schema
type ClassroomRepository struct {
	c *Client
}

func NewClassroomRepository(c *Client) *ClassroomRepository {
	return &ClassroomRepository{c: c}
}

var _ port.ClassroomRepository = (*ClassroomRepository)(nil)

func (r *ClassroomRepository) InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error) {
	var i domain.Institution
	err := r.c.pool.QueryRow(ctx, `SELECT id, slug, name, kind FROM iam.institutions WHERE slug = $1 AND is_active`, slug).
		Scan(&i.ID, &i.Slug, &i.Name, &i.Kind)
	return i, notFound(err)
}

func (r *ClassroomRepository) StaffByEmail(ctx context.Context, institutionID, email string) (domain.Staff, error) {
	s, err := scanStaff(r.c.pool.QueryRow(ctx, `SELECT `+staffColumns+` FROM people.staff st
		WHERE st.institution_id = $1 AND lower(st.email) = lower($2) AND st.status = 'active'`, institutionID, email))
	return s, academicErr(err)
}

func (r *ClassroomRepository) StudentGrades(ctx context.Context, institutionID, periodID, email string) (map[string]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id::text, coalesce(e.grade_id::text, '')
		FROM people.students s JOIN enrollment.enrollments e ON e.student_id = s.id AND e.period_id = $2 AND e.status = 'active'
		WHERE s.institution_id = $1 AND s.status = 'active' AND (lower(s.email) = lower($3) OR EXISTS (
			SELECT 1 FROM people.student_guardians sg JOIN people.guardians gu ON gu.id = sg.guardian_id
			WHERE sg.student_id = s.id AND lower(gu.email) = lower($3)))`, institutionID, periodID, email)
	if err != nil {
		return nil, academicErr(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var student, grade string
		if err := rows.Scan(&student, &grade); err != nil {
			return nil, err
		}
		out[student] = grade
	}
	return out, rows.Err()
}

func (r *ClassroomRepository) RunningPeriod(ctx context.Context, institutionID string) (string, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `SELECT id::text FROM academic.periods WHERE institution_id = $1 AND status = 'active'
		ORDER BY starts_on DESC LIMIT 1`, institutionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (r *ClassroomRepository) Periods(ctx context.Context, institutionID string) ([]domain.Period, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, name, status, starts_on, ends_on FROM academic.periods WHERE institution_id = $1 ORDER BY starts_on DESC`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Period, error) {
		var p domain.Period
		err := row.Scan(&p.ID, &p.Name, &p.Status, &p.StartsOn, &p.EndsOn)
		return p, err
	})
	if out == nil {
		out = []domain.Period{}
	}
	return out, academicErr(err)
}

// ---------- rooms ----------

const roomColumns = `r.id, r.institution_id, r.period_id, p.name, r.subject_id, s.name, s.code, l.id, l.name,
	coalesce(r.grade_id::text, ''), coalesce(g.name, ''), coalesce(g.short_name, ''), r.description,
	coalesce(r.staff_id::text, ''), coalesce(st.given_names || ' ' || st.surnames, ''), r.status, r.updated_at,
	(SELECT count(*) FROM classroom.units u WHERE u.room_id = r.id),
	(SELECT count(*) FROM classroom.items i WHERE i.room_id = r.id),
	(SELECT count(*) FROM enrollment.enrollments e WHERE e.period_id = r.period_id AND e.status = 'active'
		AND e.level_id = l.id AND e.grade_id IS NOT DISTINCT FROM r.grade_id),
	l.position, coalesce(g.position, 0), s.position`

const roomFrom = ` FROM classroom.rooms r
	JOIN academic.periods p ON p.id = r.period_id
	JOIN academic.subjects s ON s.id = r.subject_id
	JOIN academic.curricula c ON c.id = s.curriculum_id
	JOIN academic.levels l ON l.id = c.level_id
	LEFT JOIN academic.grades g ON g.id = r.grade_id
	LEFT JOIN people.staff st ON st.id = r.staff_id`

func scanRoom(row pgx.Row) (domain.Room, error) {
	var x domain.Room
	var units, items, students int64
	var lp, gp, sp int16
	err := row.Scan(&x.ID, &x.InstitutionID, &x.PeriodID, &x.PeriodName, &x.SubjectID, &x.SubjectName, &x.SubjectCode, &x.LevelID, &x.LevelName,
		&x.GradeID, &x.GradeName, &x.GradeShort, &x.Description, &x.StaffID, &x.StaffName, &x.Status, &x.UpdatedAt, &units, &items, &students, &lp, &gp, &sp)
	x.Units, x.Items, x.Students = int(units), int(items), int(students)
	x.LevelPosition, x.GradePosition, x.SubjectPosition = int(lp), int(gp), int(sp)
	return x, err
}

func (r *ClassroomRepository) Rooms(ctx context.Context, institutionID string, f domain.RoomFilter) ([]domain.Room, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+roomColumns+roomFrom+`
		WHERE r.institution_id = $1 AND r.period_id = $2
		  AND ($3 = '' OR r.staff_id = $3::uuid OR EXISTS (
			SELECT 1 FROM academic.assignments a JOIN academic.sections sec ON sec.id = a.section_id
			WHERE a.period_id = r.period_id AND a.subject_id = r.subject_id AND a.staff_id = $3::uuid
			  AND sec.grade_id IS NOT DISTINCT FROM r.grade_id))
		  AND ($4::uuid[] IS NULL OR r.grade_id = ANY($4))
		  AND (NOT $5 OR r.status = 'published')
		ORDER BY l.position, g.position NULLS FIRST, s.position`,
		institutionID, f.PeriodID, f.StaffID, nilIfNoIDs(f.GradeIDs), f.PublishedOnly)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Room, error) { return scanRoom(row) })
	if out == nil {
		out = []domain.Room{}
	}
	return out, academicErr(err)
}

// nilIfNoIDs keeps a nil filter as SQL NULL (no filter) and an empty one as an empty array (matches nothing)
func nilIfNoIDs(ids []string) []string {
	if ids == nil {
		return nil
	}
	return ids
}

func (r *ClassroomRepository) Room(ctx context.Context, institutionID, roomID string) (domain.Room, error) {
	x, err := scanRoom(r.c.pool.QueryRow(ctx, `SELECT `+roomColumns+roomFrom+` WHERE r.institution_id = $1 AND r.id = $2`, institutionID, roomID))
	return x, academicErr(err)
}

func (r *ClassroomRepository) EnsureRooms(ctx context.Context, institutionID, periodID string) (int, error) {
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO classroom.rooms (institution_id, period_id, subject_id, grade_id, staff_id)
		SELECT DISTINCT ON (sg.subject_id, sg.grade_id) $1::uuid, p.id, sg.subject_id, sg.grade_id,
			(SELECT a.staff_id FROM academic.assignments a JOIN academic.sections sec ON sec.id = a.section_id
			 WHERE a.period_id = p.id AND a.subject_id = sg.subject_id AND sec.grade_id = sg.grade_id AND a.staff_id IS NOT NULL
			 ORDER BY sec.name LIMIT 1)
		FROM academic.periods p
		JOIN academic.curricula c ON c.institution_id = p.institution_id AND c.status = 'active'
		JOIN academic.subjects s ON s.curriculum_id = c.id AND s.kind <> 'tutoring'
		JOIN academic.subject_grades sg ON sg.subject_id = s.id
		WHERE p.institution_id = $1 AND p.id = $2
		  AND EXISTS (SELECT 1 FROM academic.sections sec WHERE sec.period_id = p.id AND sec.grade_id = sg.grade_id)
		ON CONFLICT DO NOTHING`, institutionID, periodID)
	if err != nil {
		return 0, academicErr(err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *ClassroomRepository) UpdateRoom(ctx context.Context, institutionID string, x domain.Room) (domain.Room, error) {
	// The teacher in charge must be active staff of the institution
	tag, err := r.c.pool.Exec(ctx, `UPDATE classroom.rooms SET description = $3, status = $4, staff_id = $5::uuid
		WHERE institution_id = $1 AND id = $2
		  AND ($5::uuid IS NULL OR EXISTS (SELECT 1 FROM people.staff WHERE id = $5::uuid AND institution_id = $1 AND status = 'active'))`,
		institutionID, x.ID, x.Description, x.Status, nilIfEmpty(x.StaffID))
	if err != nil {
		return domain.Room{}, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Room{}, domain.ErrNotFound
	}
	return r.Room(ctx, institutionID, x.ID)
}

func (r *ClassroomRepository) TeachesRoom(ctx context.Context, institutionID, roomID, staffID string) (bool, error) {
	var ok bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM classroom.rooms r WHERE r.institution_id = $1 AND r.id = $2 AND (r.staff_id = $3 OR EXISTS (
		SELECT 1 FROM academic.assignments a JOIN academic.sections sec ON sec.id = a.section_id
		WHERE a.period_id = r.period_id AND a.subject_id = r.subject_id AND a.staff_id = $3 AND sec.grade_id IS NOT DISTINCT FROM r.grade_id)))`,
		institutionID, roomID, staffID).Scan(&ok)
	return ok, academicErr(err)
}

// ---------- units and items ----------

const unitColumns = `u.id, u.room_id, u.position, u.title, u.summary, u.opens_on, u.published`

func scanUnit(row pgx.Row) (domain.Unit, error) {
	var u domain.Unit
	var pos int16
	err := row.Scan(&u.ID, &u.RoomID, &pos, &u.Title, &u.Summary, &u.OpensOn, &u.Published)
	u.Position = int(pos)
	u.Items = []domain.Item{}
	return u, err
}

// itemColumns read the library's text, link and files when the item comes from it; $1 is the institution, $2 the
// student whose views are marked (” for none), $3 whether to count views
const itemColumns = `i.id, i.room_id, i.unit_id, i.position, i.kind, i.title,
	CASE WHEN l.id IS NULL THEN i.body ELSE l.body END, CASE WHEN l.id IS NULL THEN i.url ELSE l.url END,
	i.complementary, i.starts_at, i.duration_minutes, i.recording_url, coalesce(i.library_item_id::text, ''), i.published,
	(SELECT count(*) FROM attachments.files f WHERE f.institution_id = $1 AND
		((f.owner_kind = 'classroom_item' AND f.owner_id = i.id) OR (f.owner_kind = 'library_item' AND f.owner_id = i.library_item_id))),
	($2 <> '' AND EXISTS (SELECT 1 FROM classroom.item_views v WHERE v.item_id = i.id AND v.student_id::text = $2)),
	CASE WHEN $3 THEN (SELECT count(*) FROM classroom.item_views v WHERE v.item_id = i.id) ELSE 0 END`

const itemFrom = ` FROM classroom.items i JOIN classroom.rooms r ON r.id = i.room_id LEFT JOIN classroom.library_items l ON l.id = i.library_item_id`

func scanItem(row pgx.Row) (domain.Item, error) {
	var it domain.Item
	var pos int16
	var duration *int16
	var files, views int64
	err := row.Scan(&it.ID, &it.RoomID, &it.UnitID, &pos, &it.Kind, &it.Title, &it.Body, &it.URL, &it.Complementary, &it.StartsAt, &duration,
		&it.RecordingURL, &it.LibraryItemID, &it.Published, &files, &it.Viewed, &views)
	it.Position, it.Files, it.Views = int(pos), int(files), int(views)
	if duration != nil {
		d := int(*duration)
		it.DurationMinutes = &d
	}
	return it, err
}

func (r *ClassroomRepository) Units(ctx context.Context, roomID string, withViews bool, studentID string) ([]domain.Unit, error) {
	var institutionID string
	if err := r.c.pool.QueryRow(ctx, `SELECT institution_id::text FROM classroom.rooms WHERE id = $1`, roomID).Scan(&institutionID); err != nil {
		return nil, academicErr(err)
	}
	rows, err := r.c.pool.Query(ctx, `SELECT `+unitColumns+` FROM classroom.units u WHERE u.room_id = $1 ORDER BY u.position, u.created_at`, roomID)
	if err != nil {
		return nil, academicErr(err)
	}
	units, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Unit, error) { return scanUnit(row) })
	if err != nil {
		return nil, academicErr(err)
	}
	rows, err = r.c.pool.Query(ctx, `SELECT `+itemColumns+itemFrom+` WHERE r.institution_id = $1 AND i.room_id = $4 ORDER BY i.position, i.created_at`,
		institutionID, studentID, withViews, roomID)
	if err != nil {
		return nil, academicErr(err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Item, error) { return scanItem(row) })
	if err != nil {
		return nil, academicErr(err)
	}
	index := map[string]int{}
	for i, u := range units {
		index[u.ID] = i
	}
	for _, it := range items {
		if i, ok := index[it.UnitID]; ok {
			units[i].Items = append(units[i].Items, it)
		}
	}
	if units == nil {
		units = []domain.Unit{}
	}
	return units, nil
}

func (r *ClassroomRepository) Unit(ctx context.Context, institutionID, unitID string) (domain.Unit, error) {
	u, err := scanUnit(r.c.pool.QueryRow(ctx, `SELECT `+unitColumns+` FROM classroom.units u JOIN classroom.rooms r ON r.id = u.room_id
		WHERE r.institution_id = $1 AND u.id = $2`, institutionID, unitID))
	return u, academicErr(err)
}

func (r *ClassroomRepository) SaveUnit(ctx context.Context, institutionID string, u domain.Unit) (domain.Unit, error) {
	var opens *time.Time
	if u.OpensOn != nil {
		d := date(*u.OpensOn)
		opens = &d
	}
	var id string
	var err error
	if u.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO classroom.units (room_id, position, title, summary, opens_on, published)
			SELECT r.id, coalesce((SELECT max(position) + 1 FROM classroom.units WHERE room_id = r.id), 0), $3, $4, $5, $6
			FROM classroom.rooms r WHERE r.institution_id = $1 AND r.id = $2 RETURNING id`,
			institutionID, u.RoomID, u.Title, u.Summary, opens, u.Published).Scan(&id)
	} else {
		err = r.c.pool.QueryRow(ctx, `UPDATE classroom.units u SET title = $3, summary = $4, opens_on = $5, published = $6
			FROM classroom.rooms r WHERE r.id = u.room_id AND r.institution_id = $1 AND u.id = $2 RETURNING u.id`,
			institutionID, u.ID, u.Title, u.Summary, opens, u.Published).Scan(&id)
	}
	if err != nil {
		return domain.Unit{}, academicErr(err)
	}
	return r.Unit(ctx, institutionID, id)
}

func (r *ClassroomRepository) DeleteUnit(ctx context.Context, institutionID, unitID string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM classroom.units u USING classroom.rooms r WHERE r.id = u.room_id AND r.institution_id = $1 AND u.id = $2`, institutionID, unitID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

// reorder sets positions in the given order; ids must be exactly the rows of the parent
func (r *ClassroomRepository) reorder(ctx context.Context, countSQL, updateSQL, parentID string, ids []string) error {
	return pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, countSQL, parentID, ids).Scan(&n); err != nil {
			return academicErr(err)
		}
		if n != len(ids) {
			return domain.ErrNotFound
		}
		for i, id := range ids {
			if _, err := tx.Exec(ctx, updateSQL, id, i); err != nil {
				return academicErr(err)
			}
		}
		return nil
	})
}

func (r *ClassroomRepository) ReorderUnits(ctx context.Context, institutionID, roomID string, ids []string) error {
	if _, err := r.Room(ctx, institutionID, roomID); err != nil {
		return err
	}
	return r.reorder(ctx,
		`SELECT CASE WHEN (SELECT count(*) FROM classroom.units WHERE room_id = $1) = cardinality($2::uuid[])
			THEN (SELECT count(*) FROM classroom.units WHERE room_id = $1 AND id = ANY($2)) ELSE -1 END`,
		`UPDATE classroom.units SET position = $2 WHERE id = $1`, roomID, ids)
}

func (r *ClassroomRepository) CopyUnits(ctx context.Context, institutionID, fromRoomID, toRoomID string) (int, error) {
	copied := 0
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT count(*) = 2 FROM classroom.rooms WHERE institution_id = $1 AND id IN ($2, $3)`, institutionID, fromRoomID, toRoomID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return domain.ErrNotFound
		}
		var has bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM classroom.units WHERE room_id = $1)`, toRoomID).Scan(&has); err != nil {
			return err
		}
		if has {
			return domain.ErrRoomHasMaterial
		}
		// Units keep their order and title; opening dates and publication are set again for the new period
		rows, err := tx.Query(ctx, `INSERT INTO classroom.units (room_id, position, title, summary)
			SELECT $2, position, title, summary FROM classroom.units WHERE room_id = $1 ORDER BY position
			RETURNING id, position`, fromRoomID, toRoomID)
		if err != nil {
			return err
		}
		newByPos := map[int16]string{}
		for rows.Next() {
			var id string
			var pos int16
			if err := rows.Scan(&id, &pos); err != nil {
				rows.Close()
				return err
			}
			newByPos[pos] = id
			copied++
		}
		rows.Close()
		// Items keep their content and library link (live sessions keep their link; dates are edited later)
		for pos, id := range newByPos {
			if _, err := tx.Exec(ctx, `INSERT INTO classroom.items (room_id, unit_id, position, kind, title, body, url, complementary, starts_at,
				duration_minutes, recording_url, library_item_id, published, created_by)
				SELECT $2, $3, i.position, i.kind, i.title, i.body, i.url, i.complementary, i.starts_at, i.duration_minutes, '', i.library_item_id, false, i.created_by
				FROM classroom.items i JOIN classroom.units u ON u.id = i.unit_id WHERE u.room_id = $1 AND u.position = $4`,
				fromRoomID, toRoomID, id, pos); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, domain.ErrRoomHasMaterial) || errors.Is(err, domain.ErrNotFound) {
		return 0, err
	}
	return copied, academicErr(err)
}

func (r *ClassroomRepository) Item(ctx context.Context, institutionID, itemID string) (domain.Item, error) {
	it, err := scanItem(r.c.pool.QueryRow(ctx, `SELECT `+itemColumns+itemFrom+` WHERE r.institution_id = $1 AND i.id = $4`, institutionID, "", true, itemID))
	return it, academicErr(err)
}

func (r *ClassroomRepository) SaveItem(ctx context.Context, institutionID, userID string, it domain.Item) (domain.Item, error) {
	var duration *int16
	if it.DurationMinutes != nil {
		d := int16(*it.DurationMinutes)
		duration = &d
	}
	var id string
	var err error
	// The unit (and the library entry, when given) must belong to the institution
	if it.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO classroom.items (room_id, unit_id, position, kind, title, body, url, complementary, starts_at,
			duration_minutes, recording_url, library_item_id, published, created_by)
			SELECT u.room_id, u.id, coalesce((SELECT max(position) + 1 FROM classroom.items WHERE unit_id = u.id), 0), $3, $4, $5, $6, $7, $8, $9, $10, $11::uuid, $12, $13::uuid
			FROM classroom.units u JOIN classroom.rooms r ON r.id = u.room_id
			WHERE r.institution_id = $1 AND u.id = $2
			  AND ($11::uuid IS NULL OR EXISTS (SELECT 1 FROM classroom.library_items WHERE id = $11::uuid AND institution_id = $1))
			RETURNING id`,
			institutionID, it.UnitID, it.Kind, it.Title, it.Body, it.URL, it.Complementary, it.StartsAt, duration, it.RecordingURL,
			nilIfEmpty(it.LibraryItemID), it.Published, nilIfEmpty(userID)).Scan(&id)
	} else {
		err = r.c.pool.QueryRow(ctx, `UPDATE classroom.items i SET kind = $3, title = $4, body = $5, url = $6, complementary = $7, starts_at = $8,
			duration_minutes = $9, recording_url = $10, published = $11
			FROM classroom.rooms r WHERE r.id = i.room_id AND r.institution_id = $1 AND i.id = $2 RETURNING i.id`,
			institutionID, it.ID, it.Kind, it.Title, it.Body, it.URL, it.Complementary, it.StartsAt, duration, it.RecordingURL, it.Published).Scan(&id)
	}
	if err != nil {
		return domain.Item{}, academicErr(err)
	}
	return r.Item(ctx, institutionID, id)
}

func (r *ClassroomRepository) DeleteItem(ctx context.Context, institutionID, itemID string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM classroom.items i USING classroom.rooms r WHERE r.id = i.room_id AND r.institution_id = $1 AND i.id = $2`, institutionID, itemID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *ClassroomRepository) ReorderItems(ctx context.Context, institutionID, unitID string, ids []string) error {
	if _, err := r.Unit(ctx, institutionID, unitID); err != nil {
		return err
	}
	return r.reorder(ctx,
		`SELECT CASE WHEN (SELECT count(*) FROM classroom.items WHERE unit_id = $1) = cardinality($2::uuid[])
			THEN (SELECT count(*) FROM classroom.items WHERE unit_id = $1 AND id = ANY($2)) ELSE -1 END`,
		`UPDATE classroom.items SET position = $2 WHERE id = $1`, unitID, ids)
}

func (r *ClassroomRepository) RecordView(ctx context.Context, itemID, studentID string, at time.Time) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO classroom.item_views (item_id, student_id, first_at, last_at) VALUES ($1, $2, $3, $3)
		ON CONFLICT (item_id, student_id) DO UPDATE SET last_at = EXCLUDED.last_at, views = classroom.item_views.views + 1`, itemID, studentID, at)
	return academicErr(err)
}

// ---------- library ----------

const libraryColumns = `l.id, l.institution_id, l.kind, l.title, l.description, l.body, l.url, l.topic, l.created_name, l.updated_at,
	(SELECT count(*) FROM attachments.files f WHERE f.institution_id = l.institution_id AND f.owner_kind = 'library_item' AND f.owner_id = l.id),
	(SELECT count(*) FROM classroom.items i WHERE i.library_item_id = l.id)`

func scanLibraryItem(row pgx.Row) (domain.LibraryItem, error) {
	var l domain.LibraryItem
	var files, uses int64
	err := row.Scan(&l.ID, &l.InstitutionID, &l.Kind, &l.Title, &l.Description, &l.Body, &l.URL, &l.Topic, &l.CreatedName, &l.UpdatedAt, &files, &uses)
	l.Files, l.Uses = int(files), int(uses)
	return l, err
}

func (r *ClassroomRepository) Library(ctx context.Context, institutionID string) ([]domain.LibraryItem, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+libraryColumns+` FROM classroom.library_items l WHERE l.institution_id = $1 ORDER BY lower(l.topic), lower(l.title)`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LibraryItem, error) { return scanLibraryItem(row) })
	if out == nil {
		out = []domain.LibraryItem{}
	}
	return out, academicErr(err)
}

func (r *ClassroomRepository) LibraryItem(ctx context.Context, institutionID, id string) (domain.LibraryItem, error) {
	l, err := scanLibraryItem(r.c.pool.QueryRow(ctx, `SELECT `+libraryColumns+` FROM classroom.library_items l WHERE l.institution_id = $1 AND l.id = $2`, institutionID, id))
	return l, academicErr(err)
}

func (r *ClassroomRepository) SaveLibraryItem(ctx context.Context, institutionID, userID, userName string, l domain.LibraryItem) (domain.LibraryItem, error) {
	var id string
	var err error
	if l.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO classroom.library_items (institution_id, kind, title, description, body, url, topic, created_by, created_name)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::uuid, $9) RETURNING id`,
			institutionID, l.Kind, l.Title, l.Description, l.Body, l.URL, l.Topic, nilIfEmpty(userID), userName).Scan(&id)
	} else {
		err = r.c.pool.QueryRow(ctx, `UPDATE classroom.library_items SET kind = $3, title = $4, description = $5, body = $6, url = $7, topic = $8
			WHERE institution_id = $1 AND id = $2 RETURNING id`, institutionID, l.ID, l.Kind, l.Title, l.Description, l.Body, l.URL, l.Topic).Scan(&id)
	}
	if err != nil {
		return domain.LibraryItem{}, academicErr(err)
	}
	return r.LibraryItem(ctx, institutionID, id)
}

func (r *ClassroomRepository) DeleteLibraryItem(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM classroom.library_items WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

// ---------- itinerant deliveries ----------

func (r *ClassroomRepository) Deliveries(ctx context.Context, institutionID, unitID string) ([]domain.Delivery, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT u.id, s.id::text,
		s.paternal_surname || CASE WHEN s.maternal_surname <> '' THEN ' ' || s.maternal_surname ELSE '' END || ', ' || s.given_names, s.code,
		d.delivered_on, d.returned_on, coalesce(d.notes, ''), coalesce(d.recorded_by_name, '')
		FROM classroom.units u
		JOIN classroom.rooms r ON r.id = u.room_id
		JOIN enrollment.enrollments e ON e.period_id = r.period_id AND e.status = 'active' AND e.grade_id IS NOT DISTINCT FROM r.grade_id
		JOIN academic.sections sec ON sec.id = e.section_id AND sec.attention_mode = 'distance' AND sec.distance_variant = 'itinerant'
		JOIN people.students s ON s.id = e.student_id
		LEFT JOIN classroom.deliveries d ON d.unit_id = u.id AND d.student_id = s.id
		WHERE r.institution_id = $1 AND u.id = $2
		ORDER BY s.paternal_surname, s.maternal_surname, s.given_names`, institutionID, unitID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Delivery, error) {
		var d domain.Delivery
		err := row.Scan(&d.UnitID, &d.StudentID, &d.StudentName, &d.StudentCode, &d.DeliveredOn, &d.ReturnedOn, &d.Notes, &d.RecordedByName)
		return d, err
	})
	if out == nil {
		out = []domain.Delivery{}
	}
	return out, academicErr(err)
}

func (r *ClassroomRepository) SaveDelivery(ctx context.Context, institutionID string, d domain.Delivery) error {
	if d.DeliveredOn == nil {
		_, err := r.c.pool.Exec(ctx, `DELETE FROM classroom.deliveries dl USING classroom.units u, classroom.rooms r
			WHERE u.id = dl.unit_id AND r.id = u.room_id AND r.institution_id = $1 AND dl.unit_id = $2 AND dl.student_id = $3`, institutionID, d.UnitID, d.StudentID)
		return academicErr(err)
	}
	delivered := date(*d.DeliveredOn)
	var returned *time.Time
	if d.ReturnedOn != nil {
		x := date(*d.ReturnedOn)
		returned = &x
	}
	// Only students of the unit's room who are in an itinerant section
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO classroom.deliveries (unit_id, student_id, delivered_on, returned_on, notes, recorded_by_name)
		SELECT u.id, e.student_id, $4, $5, $6, $7 FROM classroom.units u
		JOIN classroom.rooms r ON r.id = u.room_id
		JOIN enrollment.enrollments e ON e.period_id = r.period_id AND e.status = 'active' AND e.grade_id IS NOT DISTINCT FROM r.grade_id AND e.student_id = $3
		JOIN academic.sections sec ON sec.id = e.section_id AND sec.attention_mode = 'distance' AND sec.distance_variant = 'itinerant'
		WHERE r.institution_id = $1 AND u.id = $2
		ON CONFLICT (unit_id, student_id) DO UPDATE SET delivered_on = EXCLUDED.delivered_on, returned_on = EXCLUDED.returned_on,
			notes = EXCLUDED.notes, recorded_by_name = EXCLUDED.recorded_by_name`,
		institutionID, d.UnitID, d.StudentID, delivered, returned, d.Notes, d.RecordedByName)
	if err != nil {
		return academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
