package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Runs against a migrated database only: LTEDU_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func testRepo(t *testing.T) (*AuthRepository, func(string)) {
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
	exec := func(sql string) {
		if _, err := c.pool.Exec(context.Background(), sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	cleanup := func() {
		exec(`DELETE FROM iam.users WHERE email LIKE '%@repo-test.gmail.com' OR email LIKE 'repo-test-%'`)
		exec(`DELETE FROM iam.institutions WHERE slug = 'repo-test'`)
	}
	cleanup()
	t.Cleanup(cleanup)
	return NewAuthRepository(c), exec
}

func TestGoogleIdentitySQL(t *testing.T) {
	repo, exec := testRepo(t)
	ctx := context.Background()
	exec(`INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test', 'Repo Test', 'cepre')`)
	exec(`INSERT INTO iam.invitations (institution_id, email, role_id)
		SELECT i.id, 'repo-test-ana@gmail.com', r.id FROM iam.institutions i, iam.roles r WHERE i.slug = 'repo-test' AND r.code = 'student'`)

	identity := port.ExternalIdentity{Provider: domain.ProviderGoogle, Subject: "repo-test-sub-ana", Email: "repo-test-ana@gmail.com"}
	id, err := repo.CreateUserFromInvitations(ctx, "repo-test-ana@gmail.com", "Ana Repo", "", &identity)
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.UserByIdentity(ctx, domain.ProviderGoogle, "repo-test-sub-ana")
	if err != nil || user.ID != id {
		t.Fatalf("by identity: %+v %v", user, err)
	}
	if user.PasswordHash != "" || user.EmailVerifiedAt == nil {
		t.Fatalf("google account: no password, email verified; got %+v", user)
	}
	roles, err := repo.Roles(ctx, id)
	if err != nil || len(roles) != 1 || roles[0].RoleCode != "student" || roles[0].Institution.Slug != "repo-test" {
		t.Fatalf("roles = %+v, %v", roles, err)
	}

	// Linking again refreshes; another account cannot take the same Google subject
	if err := repo.LinkIdentity(ctx, id, identity); err != nil {
		t.Fatalf("relink: %v", err)
	}
	exec(`INSERT INTO iam.users (email, full_name) VALUES ('repo-test-beto@gmail.com', 'Beto')`)
	beto, _ := repo.UserByEmail(ctx, "repo-test-beto@gmail.com")
	if err := repo.LinkIdentity(ctx, beto.ID, identity); !errors.Is(err, domain.ErrGoogleFailed) {
		t.Fatalf("stolen subject: err = %v", err)
	}
	// One Google account per ListerCore Edu account
	other := port.ExternalIdentity{Provider: domain.ProviderGoogle, Subject: "repo-test-sub-other", Email: "x@gmail.com"}
	if err := repo.LinkIdentity(ctx, id, other); !errors.Is(err, domain.ErrGoogleFailed) {
		t.Fatalf("second google for one account: err = %v", err)
	}
	// Verified by the link
	if err := repo.LinkIdentity(ctx, beto.ID, port.ExternalIdentity{Provider: domain.ProviderGoogle, Subject: "repo-test-sub-beto", Email: "repo-test-beto@gmail.com"}); err != nil {
		t.Fatal(err)
	}
	beto, _ = repo.UserByID(ctx, beto.ID)
	if beto.EmailVerifiedAt == nil {
		t.Fatal("linking Google verifies the email")
	}
	// Sessions accept the new method
	if _, err := repo.CompleteSignIn(ctx, port.NewSession{UserID: id, TokenHash: make([]byte, 32), MFAMethod: domain.MFAGoogle, ExpiresAt: user.EmailVerifiedAt.AddDate(0, 0, 1)}); err != nil {
		t.Fatalf("google session: %v", err)
	}
}

// Every audience (nil filter) must list everything: the public site and the managers' feed rely on it
func TestAnnouncementListWithoutAudienceFilter(t *testing.T) {
	repo, exec := testRepo(t)
	ctx := context.Background()
	exec(`INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test', 'Repo Test', 'cepre')`)
	exec(`INSERT INTO comms.announcements (institution_id, slug, kind, audience, title, body, is_public)
		SELECT id, 'repotest0000000001', 'notice', 'staff', 'Solo personal', 'Detalle de prueba largo', false FROM iam.institutions WHERE slug = 'repo-test'`)
	exec(`INSERT INTO comms.announcements (institution_id, slug, kind, audience, title, body, is_public)
		SELECT id, 'repotest0000000002', 'notice', 'community', 'Para todos', 'Detalle de prueba largo', true FROM iam.institutions WHERE slug = 'repo-test'`)
	ann := NewAnnouncementRepository(repo.c)
	inst, err := ann.InstitutionBySlug(ctx, "repo-test")
	if err != nil {
		t.Fatal(err)
	}
	all, err := ann.List(ctx, domain.AnnouncementFilter{InstitutionID: inst.ID})
	if err != nil || len(all) != 2 {
		t.Fatalf("nil audiences: got %d, %v", len(all), err)
	}
	public, _ := ann.List(ctx, domain.AnnouncementFilter{InstitutionID: inst.ID, OnlyPublic: true})
	if len(public) != 1 || public[0].Slug != "repotest0000000002" {
		t.Fatalf("public: %+v", public)
	}
	students, _ := ann.List(ctx, domain.AnnouncementFilter{InstitutionID: inst.ID, Audiences: []string{"community", "students"}})
	if len(students) != 1 {
		t.Fatalf("students: %d", len(students))
	}
}
