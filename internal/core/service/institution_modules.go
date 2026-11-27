package service

import (
	"context"
	"slices"

	"api/internal/core/domain"
)

// Optional modules per institution (RF01, EQ-02): dirección turns a module off in Configuración and, from the next
// call on, the panel hides it and the API refuses its service, with no code change and no deploy

// Modules lists the optional modules with their state; every role of the institution may read it (the panel
// builds its menu from it), and canManage tells whether this user may change them
func (s *InstitutionService) Modules(ctx context.Context, token, slug string) ([]domain.ModuleState, bool, error) {
	sc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return nil, false, err
	}
	off, err := s.repo.DisabledModules(ctx, sc.institution.ID)
	if err != nil {
		return nil, false, err
	}
	out := make([]domain.ModuleState, 0, len(domain.OptionalModules))
	for _, m := range domain.OptionalModules {
		out = append(out, domain.ModuleState{OptionalModule: m, Enabled: !slices.Contains(off, m.Key)})
	}
	return out, membersManagers[sc.role], nil
}

// DisabledModules are the keys of the optional modules the institution of the session turned off
func (s *InstitutionService) DisabledModules(ctx context.Context, token, slug string) ([]string, error) {
	sc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return nil, err
	}
	return s.repo.DisabledModules(ctx, sc.institution.ID)
}

// SetModule turns an optional module on or off; only dirección (and the platform superadmin) may, and the change is
// audited and announced to the open pages
func (s *InstitutionService) SetModule(ctx context.Context, token, slug, key string, enabled bool) (domain.ModuleState, error) {
	sc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return domain.ModuleState{}, err
	}
	if !membersManagers[sc.role] {
		return domain.ModuleState{}, domain.ErrModulesReadOnly
	}
	var module domain.OptionalModule
	for _, m := range domain.OptionalModules {
		if m.Key == key {
			module = m
		}
	}
	if module.Key == "" {
		return domain.ModuleState{}, domain.ErrModuleUnknown
	}
	if err := s.repo.SetModuleDisabled(ctx, sc.institution.ID, key, !enabled, sc.userID); err != nil {
		return domain.ModuleState{}, err
	}
	verb := "Desactivó"
	if enabled {
		verb = "Activó"
	}
	s.audit.Record(ctx, sc, AuditEntry{Module: "institution", Action: domain.AuditUpdate, Entity: "module", EntityID: key,
		Summary: verb + " el módulo " + module.Name, Changes: map[string]any{"enabled": enabled}})
	s.live.Notify(ctx, EventInstitutionChanged, map[string]string{"institution": sc.institution.Slug, "area": "modules"})
	return domain.ModuleState{OptionalModule: module, Enabled: enabled}, nil
}

// ModuleAllowed is the API-side guard of an optional module: domain.ErrModuleDisabled when the institution of the
// call turned it off. A call it cannot place (no session, no institution) passes through, so the service answers
// with its own error
func (s *InstitutionService) ModuleAllowed(ctx context.Context, token, slug, key string) error {
	sc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil || sc.role == domain.RoleSuperadmin {
		return nil
	}
	off, err := s.repo.DisabledModules(ctx, sc.institution.ID)
	if err != nil {
		return err
	}
	if slices.Contains(off, key) {
		return domain.ErrModuleDisabled
	}
	return nil
}
