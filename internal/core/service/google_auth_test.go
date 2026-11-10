package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// fakeRepo implements the slice of port.AuthRepository the Google flow touches; anything else panics
type fakeRepo struct {
	port.AuthRepository
	users      map[string]domain.User // by email
	identities map[string]string      // subject -> user id
	totp       map[string]bool        // user id -> confirmed
	invited    map[string]bool        // email
	sessions   []port.NewSession
	challenges []domain.Challenge
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: map[string]domain.User{}, identities: map[string]string{}, totp: map[string]bool{}, invited: map[string]bool{}}
}

func (r *fakeRepo) byID(id string) (domain.User, bool) {
	for _, u := range r.users {
		if u.ID == id {
			return u, true
		}
	}
	return domain.User{}, false
}

func (r *fakeRepo) UserByIdentity(_ context.Context, _, subject string) (domain.User, error) {
	if id, ok := r.identities[subject]; ok {
		u, _ := r.byID(id)
		return u, nil
	}
	return domain.User{}, domain.ErrNotFound
}

func (r *fakeRepo) UserByEmail(_ context.Context, email string) (domain.User, error) {
	if u, ok := r.users[email]; ok {
		return u, nil
	}
	return domain.User{}, domain.ErrNotFound
}

func (r *fakeRepo) UserByID(_ context.Context, id string) (domain.User, error) {
	if u, ok := r.byID(id); ok {
		return u, nil
	}
	return domain.User{}, domain.ErrNotFound
}

func (r *fakeRepo) LinkIdentity(_ context.Context, userID string, identity port.ExternalIdentity) error {
	if owner, ok := r.identities[identity.Subject]; ok && owner != userID {
		return domain.ErrGoogleFailed
	}
	r.identities[identity.Subject] = userID
	return nil
}

func (r *fakeRepo) CreateUserFromInvitations(_ context.Context, email, fullName, passwordHash string, identity *port.ExternalIdentity) (string, error) {
	if !r.invited[email] {
		return "", domain.ErrNotInvited
	}
	id := "user-" + email
	r.users[email] = domain.User{ID: id, Email: email, FullName: fullName, PasswordHash: passwordHash, Status: domain.UserActive}
	if identity != nil {
		r.identities[identity.Subject] = id
	}
	return id, nil
}

func (r *fakeRepo) TOTP(_ context.Context, userID string) (domain.TOTPFactor, error) {
	if r.totp[userID] {
		now := time.Now()
		return domain.TOTPFactor{ID: "f", UserID: userID, ConfirmedAt: &now}, nil
	}
	return domain.TOTPFactor{}, domain.ErrNotFound
}

func (r *fakeRepo) CloseChallenges(context.Context, string, string) error { return nil }

func (r *fakeRepo) CreateChallenge(_ context.Context, c domain.Challenge) (string, error) {
	r.challenges = append(r.challenges, c)
	return "c", nil
}

func (r *fakeRepo) Roles(context.Context, string) ([]domain.RoleAssignment, error) { return nil, nil }

func (r *fakeRepo) CompleteSignIn(_ context.Context, s port.NewSession) (string, error) {
	r.sessions = append(r.sessions, s)
	return "s", nil
}

type fakeGoogle struct {
	identity port.ExternalIdentity
	err      error
}

func (g fakeGoogle) Configured() bool { return true }
func (g fakeGoogle) AuthorizationURL(state, ch string) string {
	return "https://google/?state=" + state
}
func (g fakeGoogle) Exchange(context.Context, string, string) (port.ExternalIdentity, error) {
	return g.identity, g.err
}

type plainHasher struct{}

func (plainHasher) Hash(p string) (string, error)    { return "h:" + p, nil }
func (plainHasher) Verify(p, e string) (bool, error) { return e == "h:"+p, nil }

var verifier = strings.Repeat("v", 43)

func googleService(t *testing.T, repo *fakeRepo, g fakeGoogle) *AuthService {
	t.Helper()
	s, err := NewAuthService(repo, plainHasher{}, nil, nil, nil, g, AuthConfig{SessionTTL: time.Hour}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func identity(subject, email string) port.ExternalIdentity {
	return port.ExternalIdentity{Provider: domain.ProviderGoogle, Subject: subject, Email: email, FullName: "Rosa Quispe"}
}

func TestGoogleLinksExistingAccountByEmail(t *testing.T) {
	repo := newFakeRepo()
	repo.users["rosa@gmail.com"] = domain.User{ID: "u1", Email: "rosa@gmail.com", Status: domain.UserActive}
	s := googleService(t, repo, fakeGoogle{identity: identity("sub-1", "rosa@gmail.com")})

	result, err := s.GoogleCallback(context.Background(), "code", verifier, domain.RequestMeta{})
	if err != nil || result.Session == nil {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if repo.identities["sub-1"] != "u1" {
		t.Fatal("the Google account must be linked to the existing user")
	}
	if repo.sessions[0].MFAMethod != domain.MFAGoogle {
		t.Fatalf("mfa method = %s", repo.sessions[0].MFAMethod)
	}
}

func TestGoogleCreatesInvitedAccountWithoutPassword(t *testing.T) {
	repo := newFakeRepo()
	repo.invited["nuevo@gmail.com"] = true
	s := googleService(t, repo, fakeGoogle{identity: identity("sub-2", "nuevo@gmail.com")})

	result, err := s.GoogleCallback(context.Background(), "code", verifier, domain.RequestMeta{})
	if err != nil || result.Session == nil {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if u := repo.users["nuevo@gmail.com"]; u.PasswordHash != "" || u.FullName != "Rosa Quispe" {
		t.Fatalf("user = %+v", u)
	}
}

func TestGoogleRefusesWhoIsNotOnTheRoster(t *testing.T) {
	s := googleService(t, newFakeRepo(), fakeGoogle{identity: identity("sub-3", "extrano@gmail.com")})
	if _, err := s.GoogleCallback(context.Background(), "code", verifier, domain.RequestMeta{}); !errors.Is(err, domain.ErrNotInvited) {
		t.Fatalf("err = %v, want not_invited", err)
	}
}

func TestGoogleStillAsksForTOTP(t *testing.T) {
	repo := newFakeRepo()
	repo.users["rosa@gmail.com"] = domain.User{ID: "u1", Email: "rosa@gmail.com", Status: domain.UserActive}
	repo.identities["sub-1"] = "u1"
	repo.totp["u1"] = true
	s := googleService(t, repo, fakeGoogle{identity: identity("sub-1", "rosa@gmail.com")})

	result, err := s.GoogleCallback(context.Background(), "code", verifier, domain.RequestMeta{})
	if err != nil || result.Step == nil || result.Session != nil || result.Step.Method != domain.MFATOTP {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if len(repo.sessions) != 0 || repo.challenges[0].Method != domain.MFATOTP {
		t.Fatal("no session may exist before the TOTP step")
	}
}

func TestGoogleRejectsSuspendedAndFailedExchange(t *testing.T) {
	repo := newFakeRepo()
	repo.users["rosa@gmail.com"] = domain.User{ID: "u1", Email: "rosa@gmail.com", Status: domain.UserSuspended}
	s := googleService(t, repo, fakeGoogle{identity: identity("sub-1", "rosa@gmail.com")})
	if _, err := s.GoogleCallback(context.Background(), "code", verifier, domain.RequestMeta{}); !errors.Is(err, domain.ErrGoogleFailed) {
		t.Fatalf("suspended: err = %v", err)
	}

	s = googleService(t, newFakeRepo(), fakeGoogle{err: errors.New("boom")})
	if _, err := s.GoogleCallback(context.Background(), "code", verifier, domain.RequestMeta{}); !errors.Is(err, domain.ErrGoogleFailed) {
		t.Fatalf("exchange: err = %v", err)
	}
	if _, err := s.GoogleCallback(context.Background(), "code", "short", domain.RequestMeta{}); !errors.Is(err, domain.ErrGoogleFailed) {
		t.Fatalf("bad verifier: err = %v", err)
	}
}

func TestGoogleAuthorizeValidatesPKCEInputs(t *testing.T) {
	s := googleService(t, newFakeRepo(), fakeGoogle{})
	if _, err := s.GoogleAuthorize("x", verifier); !errors.Is(err, domain.ErrGoogleFailed) {
		t.Fatal("a short state must be refused")
	}
	if url, err := s.GoogleAuthorize(verifier, verifier); err != nil || !strings.Contains(url, verifier) {
		t.Fatalf("url = %q, err = %v", url, err)
	}
}
