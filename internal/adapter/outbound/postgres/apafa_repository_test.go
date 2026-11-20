package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"api/internal/core/domain"
)

// Runs against a migrated database only: LTEDU_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func TestApafaLifecycle(t *testing.T) {
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
	exec := func(sql string) {
		if _, err := c.pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`DELETE FROM iam.institutions WHERE slug = 'repo-apafa'`)
	t.Cleanup(func() { exec(`DELETE FROM iam.institutions WHERE slug = 'repo-apafa'`) })
	exec(`INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-apafa', 'Repo APAFA', 'ebe')`)
	repo := NewApafaRepository(c)
	inst, err := repo.InstitutionBySlug(ctx, "repo-apafa")
	if err != nil {
		t.Fatal(err)
	}
	if m, err := repo.Management(ctx, inst.ID); err != nil || m != domain.ManagementPrivate {
		t.Fatalf("management without a profile = %q, %v; want private", m, err)
	}

	at := time.Now().Add(72 * time.Hour).Truncate(time.Second)
	m, err := repo.CreateMeeting(ctx, domain.ApafaMeeting{InstitutionID: inst.ID, Title: "Asamblea de prueba", Kind: domain.ApafaAssembly, ScheduledAt: at,
		Modality: domain.ApafaInPerson, Place: "Patio", Audience: domain.AudienceGuardians, CreatedName: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != domain.ApafaScheduled || m.NotifiedAt != nil || !m.ScheduledAt.Equal(at) {
		t.Fatalf("created meeting: %+v", m)
	}
	// A draft is hidden from families: in the list and for its attachments
	if list, _ := repo.Meetings(ctx, inst.ID, false); len(list) != 0 {
		t.Errorf("families see %d drafts", len(list))
	}
	attachments := NewAttachmentRepository(c)
	if o, err := attachments.Owner(ctx, domain.OwnerApafaMeeting, m.ID); err != nil || !o.Checked || !o.FamilyHidden || o.InstitutionID != inst.ID {
		t.Errorf("draft owner = %+v, %v", o, err)
	}

	now := time.Now()
	n := 40
	m.NotifiedAt, m.Status, m.Minutes, m.Attendees = &now, domain.ApafaHeld, "Se aprobó todo.", &n
	if m, err = repo.UpdateMeeting(ctx, m); err != nil {
		t.Fatal(err)
	}
	if m.Status != domain.ApafaHeld || m.Attendees == nil || *m.Attendees != 40 || m.NotifiedAt == nil {
		t.Errorf("updated meeting: %+v", m)
	}
	if list, _ := repo.Meetings(ctx, inst.ID, false); len(list) != 1 {
		t.Errorf("families see %d convened meetings, want 1", len(list))
	}
	if o, _ := attachments.Owner(ctx, domain.OwnerApafaMeeting, m.ID); o.FamilyHidden {
		t.Error("a convened meeting is still hidden from families")
	}

	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	b, err := repo.SaveBoardMember(ctx, domain.ApafaBoardMember{InstitutionID: inst.ID, FullName: "Ana Prueba", Position: "president", StartsOn: from, EndsOn: from.AddDate(1, 0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	b.Position = "treasurer"
	if _, err := repo.SaveBoardMember(ctx, b); err != nil {
		t.Fatal(err)
	}
	if board, _ := repo.Board(ctx, inst.ID); len(board) != 1 || board[0].Position != "treasurer" {
		t.Errorf("board = %+v", board)
	}
	if err := repo.DeleteBoardMember(ctx, inst.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if fees, err := repo.Fees(ctx, inst.ID); err != nil || fees.Charged != 0 {
		t.Errorf("fees = %+v, %v", fees, err)
	}
	if err := repo.DeleteMeeting(ctx, inst.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Meeting(ctx, inst.ID, m.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted meeting still found: %v", err)
	}
}
