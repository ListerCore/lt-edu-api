package service

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/port"
)

const (
	auditPageSize    = 50
	auditMaxPageSize = 100
)

// Only dirección (and the platform superadmin) reads the audit log
var auditReaders = map[string]bool{domain.RoleSuperadmin: true, "director": true}

// AuditService keeps the audit log: the other services record their changes through Record, and dirección reads
// them in Configuración › Auditoría. A nil *AuditService records nothing (seeds and tests)
type AuditService struct {
	repo   port.AuditRepository
	auth   *AuthService
	logger zerolog.Logger
}

func NewAuditService(repo port.AuditRepository, auth *AuthService, logger zerolog.Logger) *AuditService {
	return &AuditService{repo: repo, auth: auth, logger: logger}
}

// AuditEntry is what a service tells the log about one change
type AuditEntry struct {
	Module   string
	Action   string
	Entity   string
	EntityID string
	Summary  string
	Changes  map[string]any
}

// Record stores the change made by the person of sc. Best effort: it outlives the request's cancellation, and a
// failure is only logged, so it never undoes the change it describes
func (s *AuditService) Record(ctx context.Context, sc institutionScope, e AuditEntry) {
	if s == nil {
		return
	}
	summary := e.Summary
	if utf8.RuneCountInString(summary) > 300 {
		summary = string([]rune(summary)[:299]) + "…"
	}
	err := s.repo.Record(context.WithoutCancel(ctx), domain.AuditEvent{InstitutionID: sc.institution.ID, UserID: sc.userID, Role: sc.role,
		Module: e.Module, Action: e.Action, Entity: e.Entity, EntityID: e.EntityID, Summary: summary, Changes: e.Changes})
	if err != nil {
		s.logger.Error().Err(err).Str("module", e.Module).Str("entity", e.Entity).Msg("audit event not recorded")
	}
}

// AuditPage is one page of the log and the cursor of the next one (empty when there is none)
type AuditPage struct {
	Events      []domain.AuditEvent
	NextAt      *time.Time
	NextID      string
	HasMore     bool
	Institution domain.Institution
}

func (s *AuditService) List(ctx context.Context, token, slug string, f domain.AuditFilter) (AuditPage, error) {
	sc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return AuditPage{}, err
	}
	if !auditReaders[sc.role] {
		return AuditPage{}, domain.ErrAuditForbidden
	}
	if f.Limit <= 0 || f.Limit > auditMaxPageSize {
		f.Limit = auditPageSize
	}
	if f.From != nil && f.To != nil && !f.To.After(*f.From) {
		return AuditPage{}, domain.Validation(map[string]string{"to": "La fecha final debe ser posterior a la inicial."})
	}
	want := f.Limit
	// One extra row tells whether another page follows
	f.Limit++
	events, err := s.repo.Events(ctx, sc.institution.ID, f)
	if err != nil {
		return AuditPage{}, err
	}
	page := AuditPage{Events: events, Institution: sc.institution}
	if len(events) > want {
		page.Events = events[:want]
		last := page.Events[want-1]
		page.NextAt, page.NextID, page.HasMore = &last.CreatedAt, last.ID, true
	}
	return page, nil
}
