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
func testInstitutionRepo(t *testing.T) (*InstitutionRepository, string, string) {
	t.Helper()
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
		if _, err := c.pool.Exec(ctx, `DELETE FROM iam.institutions WHERE slug = 'repo-test-file'`); err != nil {
			t.Fatal(err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	var instID, levelID string
	if err := c.pool.QueryRow(ctx, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-file', 'Repo Test File', 'eba') RETURNING id`).Scan(&instID); err != nil {
		t.Fatal(err)
	}
	if err := c.pool.QueryRow(ctx, `INSERT INTO academic.levels (institution_id, code, name, position, attention_modes)
		VALUES ($1, 'avanzado', 'Ciclo Avanzado', 0, '{in_person,blended,distance}') RETURNING id`, instID).Scan(&levelID); err != nil {
		t.Fatal(err)
	}
	return NewInstitutionRepository(c), instID, levelID
}

func TestInstitutionFileSQL(t *testing.T) {
	repo, instID, levelID := testInstitutionRepo(t)
	ctx := context.Background()
	issued := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	// No authorization recorded: sections are not checked
	if _, recorded, err := repo.AuthorizedModes(ctx, instID, issued); err != nil || recorded {
		t.Fatalf("empty institution: recorded=%v err=%v", recorded, err)
	}

	creation, err := repo.SaveAuthorization(ctx, domain.Authorization{InstitutionID: instID, Kind: "creation", Resolution: "RD 0001-2026", IssuedBy: "DRE Ayacucho", IssuedOn: issued})
	if err != nil || creation.ID == "" || creation.Files != 0 {
		t.Fatalf("creation: %+v %v", creation, err)
	}
	ends := issued.AddDate(1, 0, 0)
	distance, err := repo.SaveAuthorization(ctx, domain.Authorization{InstitutionID: instID, Kind: "attention_mode", LevelID: levelID, AttentionMode: "distance",
		Resolution: "RD 0002-2026", IssuedOn: issued, ExpiresOn: &ends})
	if err != nil || distance.LevelName != "Ciclo Avanzado" {
		t.Fatalf("distance: %+v %v", distance, err)
	}
	// A cycle of another institution behaves as missing
	if _, err := repo.SaveAuthorization(ctx, domain.Authorization{InstitutionID: instID, Kind: "attention_mode", LevelID: "0190f0f0-0000-7000-8000-000000000000",
		AttentionMode: "blended", Resolution: "RD 3", IssuedOn: issued}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign level: %v", err)
	}

	modes, recorded, err := repo.AuthorizedModes(ctx, instID, issued)
	if err != nil || !recorded || len(modes[levelID]) != 1 || modes[levelID][0] != "distance" {
		t.Fatalf("in force: %v %v %v", modes, recorded, err)
	}
	// After it expires it no longer authorizes, but the institution still counts as having recorded modes
	modes, recorded, _ = repo.AuthorizedModes(ctx, instID, ends.AddDate(0, 0, 1))
	if !recorded || len(modes[levelID]) != 0 {
		t.Fatalf("expired: %v %v", modes, recorded)
	}

	list, err := repo.Authorizations(ctx, instID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	if err := repo.DeleteAuthorization(ctx, instID, creation.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAuthorization(ctx, instID, creation.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted twice: %v", err)
	}

	empty, err := repo.PlatformSheet(ctx, instID)
	if err != nil || empty.HostingKind != "hosting" || empty.PlatformName != "" {
		t.Fatalf("empty sheet: %+v %v", empty, err)
	}
	saved, err := repo.SavePlatformSheet(ctx, instID, domain.PlatformSheet{PlatformName: "ListerCore Edu", URL: "https://ceba.example.pe", HostingKind: "own_server",
		ValidUntil: &ends, TechnicalLead: "Ana Quispe", UpdatedByName: "Dirección"})
	if err != nil || saved.PlatformName != "ListerCore Edu" || saved.ValidUntil == nil || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved sheet: %+v %v", saved, err)
	}
	saved.Provider = "Proveedor"
	again, err := repo.SavePlatformSheet(ctx, instID, saved)
	if err != nil || again.Provider != "Proveedor" || again.HostingKind != "own_server" {
		t.Fatalf("updated sheet: %+v %v", again, err)
	}
}

func TestInstitutionMembersSQL(t *testing.T) {
	repo, instID, _ := testInstitutionRepo(t)
	ctx := context.Background()
	if _, err := repo.c.pool.Exec(ctx, `DELETE FROM iam.users WHERE email LIKE 'repo-test-member-%'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = repo.c.pool.Exec(ctx, `DELETE FROM iam.users WHERE email LIKE 'repo-test-member-%'`) })
	if _, err := repo.c.pool.Exec(ctx, `INSERT INTO iam.users (email, full_name) VALUES ('repo-test-member-ana@gmail.com', 'Ana Member')`); err != nil {
		t.Fatal(err)
	}

	// An existing account gets the role at once; an unknown email gets an invitation
	if pending, err := repo.GrantRole(ctx, instID, "repo-test-member-ana@gmail.com", "technical_lead", ""); err != nil || pending {
		t.Fatalf("grant account: pending=%v %v", pending, err)
	}
	if pending, err := repo.GrantRole(ctx, instID, "repo-test-member-luis@gmail.com", "teacher", ""); err != nil || !pending {
		t.Fatalf("grant invitation: pending=%v %v", pending, err)
	}
	if _, err := repo.GrantRole(ctx, instID, "repo-test-member-ana@gmail.com", "technical_lead", ""); !errors.Is(err, domain.ErrMemberExists) {
		t.Fatalf("twice: %v", err)
	}

	members, err := repo.Members(ctx, instID, []string{"technical_lead", "teacher"})
	if err != nil || len(members) != 2 || members[0].Pending || members[0].FullName != "Ana Member" || !members[1].Pending {
		t.Fatalf("members: %+v %v", members, err)
	}
	if only, _ := repo.Members(ctx, instID, []string{"teacher"}); len(only) != 1 {
		t.Fatalf("role filter: %d", len(only))
	}
	if n, _ := repo.CountRole(ctx, instID, "technical_lead"); n != 1 {
		t.Fatalf("count: %d", n)
	}
	one, err := repo.Member(ctx, instID, members[1].ID)
	if err != nil || !one.Pending || one.RoleCode != "teacher" {
		t.Fatalf("member: %+v %v", one, err)
	}

	for _, m := range members {
		if err := repo.RevokeMember(ctx, instID, m.ID); err != nil {
			t.Fatal(err)
		}
	}
	if left, _ := repo.Members(ctx, instID, []string{"technical_lead", "teacher"}); len(left) != 0 {
		t.Fatalf("after revoke: %+v", left)
	}
	if err := repo.RevokeMember(ctx, instID, members[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked twice: %v", err)
	}
}
