package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ComplianceRepository is DRE compliance (phase E8): equipment, checks, report matrices and the uptime log
type ComplianceRepository struct{ c *Client }

func NewComplianceRepository(c *Client) *ComplianceRepository { return &ComplianceRepository{c: c} }

var _ port.ComplianceRepository = (*ComplianceRepository)(nil)

func filesOf(kind, col string) string {
	return `(SELECT count(*) FROM attachments.files f WHERE f.owner_kind = '` + kind + `' AND f.owner_id = ` + col + `)`
}

func uniqueErr(err error, constraint string, mapped error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == constraint {
		return mapped
	}
	return academicErr(err)
}

// ---------- rooms ----------

const roomSelect = `SELECT r.id, r.name, r.location, r.area_m2::float8, r.workstations, r.furniture, r.power_supply, r.notes,
	(SELECT count(*) FROM equipment.devices d WHERE d.room_id = r.id), ` + "%s" + ` FROM equipment.rooms r`

func (r *ComplianceRepository) Rooms(ctx context.Context, institutionID string) ([]domain.EquipmentRoom, error) {
	rows, err := r.c.pool.Query(ctx, sprintf(roomSelect, filesOf(domain.OwnerEquipmentRoom, "r.id"))+` WHERE r.institution_id = $1 ORDER BY r.name`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.EquipmentRoom, error) { return scanEquipmentRoom(row) })
}

func scanEquipmentRoom(row pgx.Row) (domain.EquipmentRoom, error) {
	var x domain.EquipmentRoom
	var ws int16
	var devices, files int64
	err := row.Scan(&x.ID, &x.Name, &x.Location, &x.AreaM2, &ws, &x.Furniture, &x.PowerSupply, &x.Notes, &devices, &files)
	x.Workstations, x.Devices, x.Files = int(ws), int(devices), int(files)
	return x, err
}

func (r *ComplianceRepository) SaveRoom(ctx context.Context, institutionID string, x domain.EquipmentRoom) (domain.EquipmentRoom, error) {
	id := x.ID
	var err error
	if id == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO equipment.rooms (institution_id, name, location, area_m2, workstations, furniture, power_supply, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, institutionID, x.Name, x.Location, x.AreaM2, x.Workstations, x.Furniture, x.PowerSupply, x.Notes).Scan(&id)
	} else {
		var tag pgconn.CommandTag
		tag, err = r.c.pool.Exec(ctx, `UPDATE equipment.rooms SET name = $3, location = $4, area_m2 = $5, workstations = $6, furniture = $7, power_supply = $8, notes = $9
			WHERE institution_id = $1 AND id = $2`, institutionID, id, x.Name, x.Location, x.AreaM2, x.Workstations, x.Furniture, x.PowerSupply, x.Notes)
		if err == nil && tag.RowsAffected() == 0 {
			return x, domain.ErrNotFound
		}
	}
	if err != nil {
		return x, uniqueErr(err, "rooms_name_key", domain.ErrRoomNameTaken)
	}
	out, err := scanEquipmentRoom(r.c.pool.QueryRow(ctx, sprintf(roomSelect, filesOf(domain.OwnerEquipmentRoom, "r.id"))+` WHERE r.institution_id = $1 AND r.id = $2`, institutionID, id))
	return out, academicErr(err)
}

func (r *ComplianceRepository) DeleteRoom(ctx context.Context, institutionID, id string) error {
	return r.delete(ctx, `DELETE FROM equipment.rooms WHERE institution_id = $1 AND id = $2`, institutionID, id)
}

func (r *ComplianceRepository) delete(ctx context.Context, sql string, args ...any) error {
	tag, err := r.c.pool.Exec(ctx, sql, args...)
	if err != nil {
		return academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---------- devices ----------

const deviceSelect = `SELECT d.id, d.code, d.kind, d.brand, d.model, d.specs, coalesce(d.room_id::text, ''), coalesce(r.name, ''), d.purpose,
	coalesce(d.staff_id::text, ''), coalesce(st.given_names || ' ' || st.surnames, ''), d.assigned_to, d.status, d.notes, ` + "%s" + `,
	(SELECT max(c.created_at) FROM equipment.checks c WHERE c.device_id = d.id AND c.kind = 'platform_access' AND c.passed)
	FROM equipment.devices d LEFT JOIN equipment.rooms r ON r.id = d.room_id LEFT JOIN people.staff st ON st.id = d.staff_id`

func scanDevice(row pgx.Row) (domain.Device, error) {
	var x domain.Device
	var files int64
	err := row.Scan(&x.ID, &x.Code, &x.Kind, &x.Brand, &x.Model, &x.Specs, &x.RoomID, &x.RoomName, &x.Purpose, &x.StaffID, &x.StaffName, &x.AssignedTo, &x.Status, &x.Notes,
		&files, &x.LastAccess)
	x.Files = int(files)
	return x, err
}

func (r *ComplianceRepository) Devices(ctx context.Context, institutionID string) ([]domain.Device, error) {
	rows, err := r.c.pool.Query(ctx, sprintf(deviceSelect, filesOf(domain.OwnerEquipmentDevice, "d.id"))+` WHERE d.institution_id = $1 ORDER BY d.code`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Device, error) { return scanDevice(row) })
}

func (r *ComplianceRepository) SaveDevice(ctx context.Context, institutionID string, x domain.Device) (domain.Device, error) {
	id := x.ID
	// The room and the staff must be the institution's own
	args := []any{institutionID, x.Code, x.Kind, x.Brand, x.Model, x.Specs, x.RoomID, x.Purpose, x.StaffID, x.AssignedTo, x.Status, x.Notes}
	const room = `(SELECT id FROM equipment.rooms WHERE institution_id = $1 AND id = nullif($7, '')::uuid)`
	const staff = `(SELECT id FROM people.staff WHERE institution_id = $1 AND id = nullif($9, '')::uuid)`
	var err error
	if id == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO equipment.devices (institution_id, code, kind, brand, model, specs, room_id, purpose, staff_id, assigned_to, status, notes)
			VALUES ($1, $2, $3, $4, $5, $6, `+room+`, $8, `+staff+`, $10, $11, $12) RETURNING id`, args...).Scan(&id)
	} else {
		var tag pgconn.CommandTag
		tag, err = r.c.pool.Exec(ctx, `UPDATE equipment.devices SET code = $2, kind = $3, brand = $4, model = $5, specs = $6, room_id = `+room+`, purpose = $8,
			staff_id = `+staff+`, assigned_to = $10, status = $11, notes = $12 WHERE institution_id = $1 AND id = $13`, append(args, id)...)
		if err == nil && tag.RowsAffected() == 0 {
			return x, domain.ErrNotFound
		}
	}
	if err != nil {
		return x, uniqueErr(err, "devices_code_key", domain.ErrDeviceCodeTaken)
	}
	out, err := scanDevice(r.c.pool.QueryRow(ctx, sprintf(deviceSelect, filesOf(domain.OwnerEquipmentDevice, "d.id"))+` WHERE d.institution_id = $1 AND d.id = $2`, institutionID, id))
	return out, academicErr(err)
}

func (r *ComplianceRepository) DeleteDevice(ctx context.Context, institutionID, id string) error {
	return r.delete(ctx, `DELETE FROM equipment.devices WHERE institution_id = $1 AND id = $2`, institutionID, id)
}

// ---------- checks ----------

const checkSelect = `SELECT c.id, c.kind, coalesce(c.room_id::text, ''), coalesce(r.name, ''), coalesce(c.device_id::text, ''), coalesce(d.code, ''), c.checked_on,
	c.provider, c.link_kind, c.download_mbps::float8, c.upload_mbps::float8, c.latency_ms, c.profile, c.passed, c.result, c.checked_by_name, ` + "%s" + `
	FROM equipment.checks c LEFT JOIN equipment.rooms r ON r.id = c.room_id LEFT JOIN equipment.devices d ON d.id = c.device_id`

func scanCheck(row pgx.Row) (domain.EquipmentCheck, error) {
	var x domain.EquipmentCheck
	var latency *int32
	var files int64
	err := row.Scan(&x.ID, &x.Kind, &x.RoomID, &x.RoomName, &x.DeviceID, &x.DeviceCode, &x.CheckedOn, &x.Provider, &x.LinkKind, &x.DownloadMbps, &x.UploadMbps, &latency,
		&x.Profile, &x.Passed, &x.Result, &x.CheckedByName, &files)
	if latency != nil {
		v := int(*latency)
		x.LatencyMs = &v
	}
	x.Files = int(files)
	return x, err
}

func (r *ComplianceRepository) Checks(ctx context.Context, institutionID string, limit int) ([]domain.EquipmentCheck, error) {
	rows, err := r.c.pool.Query(ctx, sprintf(checkSelect, filesOf(domain.OwnerEquipmentCheck, "c.id"))+` WHERE c.institution_id = $1 ORDER BY c.checked_on DESC, c.created_at DESC LIMIT $2`,
		institutionID, limit)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.EquipmentCheck, error) { return scanCheck(row) })
}

func (r *ComplianceRepository) AddCheck(ctx context.Context, institutionID string, x domain.EquipmentCheck) (domain.EquipmentCheck, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO equipment.checks (institution_id, kind, room_id, device_id, checked_on, provider, link_kind, download_mbps, upload_mbps,
			latency_ms, profile, passed, result, checked_by_name)
		VALUES ($1, $2, (SELECT id FROM equipment.rooms WHERE institution_id = $1 AND id = nullif($3, '')::uuid),
			(SELECT id FROM equipment.devices WHERE institution_id = $1 AND id = nullif($4, '')::uuid), $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id`,
		institutionID, x.Kind, x.RoomID, x.DeviceID, x.CheckedOn, x.Provider, x.LinkKind, x.DownloadMbps, x.UploadMbps, x.LatencyMs, x.Profile, x.Passed, x.Result, x.CheckedByName).Scan(&id)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23514" {
			return x, domain.ErrNotFound
		}
		return x, academicErr(err)
	}
	out, err := scanCheck(r.c.pool.QueryRow(ctx, sprintf(checkSelect, filesOf(domain.OwnerEquipmentCheck, "c.id"))+` WHERE c.institution_id = $1 AND c.id = $2`, institutionID, id))
	return out, academicErr(err)
}

func (r *ComplianceRepository) DeleteCheck(ctx context.Context, institutionID, id string) error {
	return r.delete(ctx, `DELETE FROM equipment.checks WHERE institution_id = $1 AND id = $2`, institutionID, id)
}

// ---------- reports and matrices ----------

func (r *ComplianceRepository) Report(ctx context.Context, institutionID, kind string) (domain.ComplianceReport, error) {
	x := domain.ComplianceReport{Kind: kind}
	err := r.c.pool.QueryRow(ctx, `SELECT number, file_number, city, location, evaluated_on, general_result, limitations, conclusion, updated_by_name, updated_at
		FROM compliance.reports WHERE institution_id = $1 AND kind = $2`, institutionID, kind).
		Scan(&x.Number, &x.FileNumber, &x.City, &x.Location, &x.EvaluatedOn, &x.GeneralResult, &x.Limitations, &x.Conclusion, &x.UpdatedByName, &x.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, nil
	}
	return x, academicErr(err)
}

func (r *ComplianceRepository) SaveReport(ctx context.Context, institutionID string, x domain.ComplianceReport) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO compliance.reports (institution_id, kind, number, file_number, city, location, evaluated_on, general_result, limitations, conclusion, updated_by_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (institution_id, kind) DO UPDATE SET number = EXCLUDED.number, file_number = EXCLUDED.file_number, city = EXCLUDED.city, location = EXCLUDED.location,
			evaluated_on = EXCLUDED.evaluated_on, general_result = EXCLUDED.general_result, limitations = EXCLUDED.limitations, conclusion = EXCLUDED.conclusion,
			updated_by_name = EXCLUDED.updated_by_name`,
		institutionID, x.Kind, x.Number, x.FileNumber, x.City, x.Location, x.EvaluatedOn, x.GeneralResult, x.Limitations, x.Conclusion, x.UpdatedByName)
	return academicErr(err)
}

func (r *ComplianceRepository) EnsureItems(ctx context.Context, institutionID string, items []domain.ComplianceItem) error {
	batch := &pgx.Batch{}
	for _, it := range items {
		batch.Queue(`INSERT INTO compliance.items (institution_id, report, section, code, position, element, module, route)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (institution_id, report, code) DO NOTHING`,
			institutionID, it.Report, it.Section, it.Code, it.Position, it.Element, it.Module, it.Route)
	}
	return academicErr(r.c.pool.SendBatch(ctx, batch).Close())
}

const itemSelect = `SELECT i.id, i.report, i.section, i.code, i.position, i.element, i.module, i.route, i.annex, i.folio, i.status, i.notes, i.verified_by_name,
	i.verified_at, ` + "%s" + ` FROM compliance.items i`

func scanComplianceItem(row pgx.Row) (domain.ComplianceItem, error) {
	var x domain.ComplianceItem
	var pos int16
	var files int64
	err := row.Scan(&x.ID, &x.Report, &x.Section, &x.Code, &pos, &x.Element, &x.Module, &x.Route, &x.Annex, &x.Folio, &x.Status, &x.Notes, &x.VerifiedByName, &x.VerifiedAt, &files)
	x.Position, x.Files = int(pos), int(files)
	return x, err
}

func (r *ComplianceRepository) Items(ctx context.Context, institutionID, report string) ([]domain.ComplianceItem, error) {
	rows, err := r.c.pool.Query(ctx, sprintf(itemSelect, filesOf(domain.OwnerComplianceItem, "i.id"))+` WHERE i.institution_id = $1 AND i.report = $2 ORDER BY i.position`, institutionID, report)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ComplianceItem, error) { return scanComplianceItem(row) })
}

func (r *ComplianceRepository) Item(ctx context.Context, institutionID, id string) (domain.ComplianceItem, error) {
	x, err := scanComplianceItem(r.c.pool.QueryRow(ctx, sprintf(itemSelect, filesOf(domain.OwnerComplianceItem, "i.id"))+` WHERE i.institution_id = $1 AND i.id = $2`, institutionID, id))
	return x, academicErr(err)
}

func (r *ComplianceRepository) SaveItem(ctx context.Context, institutionID string, it domain.ComplianceItem) error {
	return r.delete(ctx, `UPDATE compliance.items SET module = $3, route = $4, annex = $5, folio = $6, notes = $7 WHERE institution_id = $1 AND id = $2`,
		institutionID, it.ID, it.Module, it.Route, it.Annex, it.Folio, it.Notes)
}

func (r *ComplianceRepository) SetItemStatus(ctx context.Context, institutionID, id, status, by string, at time.Time) error {
	var stamp *time.Time
	if status == domain.ItemVerified {
		stamp = &at
	} else {
		by = ""
	}
	return r.delete(ctx, `UPDATE compliance.items SET status = $3, verified_by_name = $4, verified_at = $5 WHERE institution_id = $1 AND id = $2`,
		institutionID, id, status, by, stamp)
}

// ---------- uptime ----------

func (r *ComplianceRepository) UptimeTargets(ctx context.Context) ([]domain.UptimeTarget, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT institution_id::text, url FROM academic.platform_sheets WHERE url ~ '^https?://'`)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.UptimeTarget, error) {
		var t domain.UptimeTarget
		err := row.Scan(&t.InstitutionID, &t.URL)
		return t, err
	})
}

func (r *ComplianceRepository) RecordUptime(ctx context.Context, institutionID string, c domain.UptimeCheck) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO compliance.uptime_checks (institution_id, url, checked_at, status_code, response_ms, ok, error) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		institutionID, c.URL, c.CheckedAt, c.StatusCode, c.ResponseMs, c.OK, c.Error)
	return academicErr(err)
}

func (r *ComplianceRepository) Uptime(ctx context.Context, institutionID string, since time.Time) ([]domain.UptimeCheck, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT url, checked_at, status_code, response_ms, ok, error FROM compliance.uptime_checks
		WHERE institution_id = $1 AND checked_at >= $2 ORDER BY checked_at DESC LIMIT 5000`, institutionID, since)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.UptimeCheck, error) {
		var c domain.UptimeCheck
		var status *int16
		var ms *int32
		err := row.Scan(&c.URL, &c.CheckedAt, &status, &ms, &c.OK, &c.Error)
		if status != nil {
			v := int(*status)
			c.StatusCode = &v
		}
		if ms != nil {
			v := int(*ms)
			c.ResponseMs = &v
		}
		return c, err
	})
}

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }
