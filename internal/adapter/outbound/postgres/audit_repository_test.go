package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"api/internal/core/domain"
)

// Runs against a migrated database only: LTEDU_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func TestAuditEventsSQL(t *testing.T) {
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
	cleanup := func() { _, _ = c.pool.Exec(ctx, `DELETE FROM iam.institutions WHERE slug = 'repo-test-audit'`) }
	cleanup()
	t.Cleanup(cleanup)
	var instID string
	if err := c.pool.QueryRow(ctx, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-audit', 'Repo Test Audit', 'eba') RETURNING id`).Scan(&instID); err != nil {
		t.Fatal(err)
	}
	repo := NewAuditRepository(c)
	for i, m := range []string{"institution", "structure", "structure"} {
		if err := repo.Record(ctx, domain.AuditEvent{InstitutionID: instID, UserName: "Dirección", Role: "director", Module: m, Action: "update",
			Entity: "section", EntityID: "x", Summary: "Cambio " + string(rune('A'+i)), Changes: map[string]any{"name": []string{"A", "B"}}}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := repo.Events(ctx, instID, domain.AuditFilter{Limit: 10})
	if err != nil || len(all) != 3 || all[0].Summary != "Cambio C" || all[0].Changes["name"] == nil {
		t.Fatalf("all: %+v %v", all, err)
	}
	structure, _ := repo.Events(ctx, instID, domain.AuditFilter{Module: "structure", Limit: 10})
	if len(structure) != 2 {
		t.Fatalf("module filter: %d", len(structure))
	}
	// Paging backwards from the newest
	older, err := repo.Events(ctx, instID, domain.AuditFilter{BeforeAt: &all[0].CreatedAt, BeforeID: all[0].ID, Limit: 10})
	if err != nil || len(older) != 2 || older[0].ID != all[1].ID {
		t.Fatalf("page: %+v %v", older, err)
	}
	future := time.Now().Add(time.Hour)
	if none, _ := repo.Events(ctx, instID, domain.AuditFilter{From: &future, Limit: 10}); len(none) != 0 {
		t.Fatalf("from filter: %d", len(none))
	}
	// Append-only
	if _, err := c.pool.Exec(ctx, `UPDATE audit.events SET summary = 'x' WHERE institution_id = $1`, instID); err == nil {
		t.Fatal("events could be edited")
	}
}
