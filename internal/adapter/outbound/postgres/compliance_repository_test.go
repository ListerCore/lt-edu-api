package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"api/internal/core/domain"
)

// Runs against a migrated database only: LTEDU_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func TestComplianceSQL(t *testing.T) {
	dsn := os.Getenv("LTEDU_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LTEDU_TEST_DATABASE_URL not set")
	}
	c, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx := context.Background()
	cleanup := func() {
		_, _ = c.pool.Exec(ctx, `DELETE FROM iam.institutions WHERE slug IN ('repo-test-compliance', 'repo-test-compliance-2')`)
	}
	cleanup()
	t.Cleanup(cleanup)
	var inst, other, staff string
	q := func(dest *string, sql string, args ...any) {
		t.Helper()
		if err := c.pool.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	q(&inst, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-compliance', 'CEBA Prueba', 'eba') RETURNING id`)
	q(&other, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-compliance-2', 'Otro', 'eba') RETURNING id`)
	q(&staff, `INSERT INTO people.staff (institution_id, code, document_type, document_number, given_names, surnames, position) VALUES ($1, 'T-1', 'dni', '42000001', 'Ana', 'Rojas', 'Tutora') RETURNING id`, inst)
	r := NewComplianceRepository(c)

	area := 24.5
	room, err := r.SaveRoom(ctx, inst, domain.EquipmentRoom{Name: "Sala de tutoría virtual", Location: "Segundo piso", AreaM2: &area, Workstations: 4})
	if err != nil || room.Workstations != 4 || room.AreaM2 == nil || *room.AreaM2 != 24.5 {
		t.Fatalf("room: %+v %v", room, err)
	}
	if _, err := r.SaveRoom(ctx, inst, domain.EquipmentRoom{Name: "Sala de tutoría virtual"}); err != domain.ErrRoomNameTaken {
		t.Fatalf("duplicate room: %v", err)
	}
	if _, err := r.SaveRoom(ctx, other, domain.EquipmentRoom{ID: room.ID, Name: "x"}); err != domain.ErrNotFound {
		t.Fatalf("foreign room: %v", err)
	}
	dev, err := r.SaveDevice(ctx, inst, domain.Device{Code: "PC-01", Kind: "desktop", Brand: "Lenovo", Model: "ThinkCentre", RoomID: room.ID, Purpose: "virtual_tutoring", StaffID: staff, Status: "operational"})
	if err != nil || dev.RoomName != "Sala de tutoría virtual" || dev.StaffName != "Ana Rojas" {
		t.Fatalf("device: %+v %v", dev, err)
	}
	if _, err := r.SaveDevice(ctx, inst, domain.Device{Code: "PC-01", Kind: "laptop", Purpose: "shared", Status: "pending"}); err != domain.ErrDeviceCodeTaken {
		t.Fatalf("duplicate code: %v", err)
	}
	// A room or staff of another institution is dropped, not linked
	foreign, err := r.SaveDevice(ctx, other, domain.Device{Code: "LAP-01", Kind: "laptop", RoomID: room.ID, StaffID: staff, Purpose: "shared", Status: "operational"})
	if err != nil || foreign.RoomID != "" || foreign.StaffID != "" {
		t.Fatalf("foreign links: %+v %v", foreign, err)
	}
	down := 95.5
	lat := 18
	conn, err := r.AddCheck(ctx, inst, domain.EquipmentCheck{Kind: "connectivity", RoomID: room.ID, CheckedOn: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), Provider: "Movistar", LinkKind: "fiber",
		DownloadMbps: &down, LatencyMs: &lat, Passed: true, CheckedByName: "Técnico"})
	if err != nil || conn.RoomName != "Sala de tutoría virtual" || conn.LatencyMs == nil || *conn.LatencyMs != 18 {
		t.Fatalf("connectivity: %+v %v", conn, err)
	}
	if _, err := r.AddCheck(ctx, inst, domain.EquipmentCheck{Kind: "platform_access", CheckedOn: time.Now(), Passed: true}); err != domain.ErrNotFound {
		t.Fatalf("access without device: %v", err)
	}
	if _, err := r.AddCheck(ctx, inst, domain.EquipmentCheck{Kind: "platform_access", DeviceID: dev.ID, CheckedOn: time.Now(), Profile: "tutor", Passed: true}); err != nil {
		t.Fatal(err)
	}
	devices, _ := r.Devices(ctx, inst)
	if len(devices) != 1 || devices[0].LastAccess == nil {
		t.Fatalf("last access: %+v", devices)
	}
	if rooms, _ := r.Rooms(ctx, inst); len(rooms) != 1 || rooms[0].Devices != 1 {
		t.Fatalf("rooms: %+v", rooms)
	}
	if checks, _ := r.Checks(ctx, inst, 10); len(checks) != 2 {
		t.Fatalf("checks: %d", len(checks))
	}

	// Matrices: ensured once, kept when ensured again
	items := []domain.ComplianceItem{{Report: "platform", Section: "informative", Code: "a", Position: 1, Element: "Ficha", Module: "M", Route: "/r"},
		{Report: "platform", Section: "classroom", Code: "b", Position: 2, Element: "Foros"}}
	if err := r.EnsureItems(ctx, inst, items); err != nil {
		t.Fatal(err)
	}
	list, _ := r.Items(ctx, inst, "platform")
	if len(list) != 2 || list[0].Code != "a" {
		t.Fatalf("items: %+v", list)
	}
	it := list[0]
	it.Annex, it.Folio, it.Module = "03", "12", "Matrícula"
	if err := r.SaveItem(ctx, inst, it); err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureItems(ctx, inst, items); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.Item(ctx, inst, it.ID); again.Module != "Matrícula" || again.Annex != "03" {
		t.Fatalf("ensure kept edits: %+v", again)
	}
	at := time.Now()
	if err := r.SetItemStatus(ctx, inst, it.ID, domain.ItemVerified, "Técnico", at); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.Item(ctx, inst, it.ID); v.VerifiedAt == nil || v.VerifiedByName != "Técnico" {
		t.Fatalf("verified: %+v", v)
	}
	if err := r.SetItemStatus(ctx, inst, it.ID, domain.ItemPending, "Técnico", at); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.Item(ctx, inst, it.ID); v.VerifiedAt != nil || v.VerifiedByName != "" {
		t.Fatalf("back to pending: %+v", v)
	}
	if err := r.SetItemStatus(ctx, other, it.ID, domain.ItemPending, "x", at); err != domain.ErrNotFound {
		t.Fatalf("foreign item: %v", err)
	}

	// Report data
	if rep, _ := r.Report(ctx, inst, "platform"); rep.UpdatedAt != nil {
		t.Fatal("report before saving")
	}
	on := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	if err := r.SaveReport(ctx, inst, domain.ComplianceReport{Kind: "platform", Number: "001-2026", FileNumber: "2026-0013636", EvaluatedOn: &on, Conclusion: "Conforme", UpdatedByName: "Técnico"}); err != nil {
		t.Fatal(err)
	}
	if rep, _ := r.Report(ctx, inst, "platform"); rep.Number != "001-2026" || rep.EvaluatedOn == nil || rep.UpdatedAt == nil {
		t.Fatalf("report: %+v", rep)
	}

	// Uptime
	code, ms := 200, 120
	if err := r.RecordUptime(ctx, inst, domain.UptimeCheck{URL: "https://ceba.example", CheckedAt: time.Now(), StatusCode: &code, ResponseMs: &ms, OK: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordUptime(ctx, inst, domain.UptimeCheck{URL: "https://ceba.example", CheckedAt: time.Now(), Error: "timeout"}); err != nil {
		t.Fatal(err)
	}
	if up, _ := r.Uptime(ctx, inst, time.Now().Add(-time.Hour)); len(up) != 2 || up[0].OK || up[1].StatusCode == nil || *up[1].StatusCode != 200 {
		t.Fatalf("uptime: %+v", up)
	}
	if err := r.DeleteDevice(ctx, other, dev.ID); err != domain.ErrNotFound {
		t.Fatalf("foreign delete: %v", err)
	}
}
