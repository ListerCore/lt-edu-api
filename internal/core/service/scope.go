package service

import (
	"context"

	"api/internal/core/domain"
)

// institutionScope is the institution a panel request works on and the role the person holds there
type institutionScope struct {
	institution domain.Institution
	role        string
	userID      string
}

// resolveInstitution picks the institution of a panel request: the one of the active role, or institutionSlug when
// the person holds a role there; the platform superadmin can open any institution by slug
func resolveInstitution(ctx context.Context, auth *AuthService, bySlug func(context.Context, string) (domain.Institution, error), token, institutionSlug string) (institutionScope, error) {
	me, err := auth.Principal(ctx, token)
	if err != nil {
		return institutionScope{}, err
	}
	if me.ActiveRole != nil && me.ActiveRole.Institution != nil && (institutionSlug == "" || institutionSlug == me.ActiveRole.Institution.Slug) {
		return institutionScope{institution: *me.ActiveRole.Institution, role: me.ActiveRole.RoleCode, userID: me.User.ID}, nil
	}
	superadmin := false
	for _, r := range me.Roles {
		if r.RoleCode == domain.RoleSuperadmin {
			superadmin = true
		}
		if r.Institution != nil && r.Institution.Slug == institutionSlug {
			return institutionScope{institution: *r.Institution, role: r.RoleCode, userID: me.User.ID}, nil
		}
	}
	if !superadmin {
		// Institution roles always work on their own institution, whatever the request names
		if me.ActiveRole != nil && me.ActiveRole.Institution != nil {
			return institutionScope{institution: *me.ActiveRole.Institution, role: me.ActiveRole.RoleCode, userID: me.User.ID}, nil
		}
		return institutionScope{}, domain.ErrForbidden
	}
	if institutionSlug == "" {
		return institutionScope{}, domain.ErrInstitutionRequired
	}
	inst, err := bySlug(ctx, institutionSlug)
	if err != nil {
		return institutionScope{}, err
	}
	return institutionScope{institution: inst, role: domain.RoleSuperadmin, userID: me.User.ID}, nil
}
