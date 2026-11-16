package service

import (
	"testing"

	"api/internal/core/domain"
)

func TestValidateAuthorization(t *testing.T) {
	issued := day("2026-03-02")
	before := day("2026-01-01")
	cases := []struct {
		name  string
		kind  string
		in    domain.Authorization
		field string
	}{
		{"creation", domain.KindEBA, domain.Authorization{Kind: "creation", Resolution: "RD N.° 0123-2026-DREA", IssuedOn: issued}, ""},
		{"distance in a CEBA", domain.KindEBA, domain.Authorization{Kind: "attention_mode", AttentionMode: "distance", Resolution: "RD 456", IssuedOn: issued}, ""},
		{"modes only in a CEBA", domain.KindEBR, domain.Authorization{Kind: "attention_mode", AttentionMode: "distance", Resolution: "RD 456", IssuedOn: issued}, "kind"},
		{"mode required", domain.KindEBA, domain.Authorization{Kind: "attention_mode", Resolution: "RD 456", IssuedOn: issued}, "attentionMode"},
		{"unknown kind", domain.KindEBA, domain.Authorization{Kind: "license", Resolution: "RD 456", IssuedOn: issued}, "kind"},
		{"resolution required", domain.KindEBA, domain.Authorization{Kind: "creation", Resolution: "  ", IssuedOn: issued}, "resolution"},
		{"date required", domain.KindEBA, domain.Authorization{Kind: "creation", Resolution: "RD 456"}, "issuedOn"},
		{"expires before issued", domain.KindEBA, domain.Authorization{Kind: "creation", Resolution: "RD 456", IssuedOn: issued, ExpiresOn: &before}, "expiresOn"},
	}
	for _, c := range cases {
		in := c.in
		fields := validateAuthorization(c.kind, &in)
		if c.field == "" && len(fields) > 0 {
			t.Errorf("%s: unexpected %v", c.name, fields)
		}
		if c.field != "" && fields[c.field] == "" {
			t.Errorf("%s: want error on %s, got %v", c.name, c.field, fields)
		}
	}
	// A non-mode authorization never keeps a cycle or form
	a := domain.Authorization{Kind: "creation", LevelID: "x", AttentionMode: "distance", Resolution: "RD 1", IssuedOn: issued}
	validateAuthorization(domain.KindEBA, &a)
	if a.LevelID != "" || a.AttentionMode != "" {
		t.Fatalf("creation kept level %q mode %q", a.LevelID, a.AttentionMode)
	}
}

func TestAuthorizationInForce(t *testing.T) {
	ends := day("2026-12-31")
	a := domain.Authorization{IssuedOn: day("2026-03-01"), ExpiresOn: &ends}
	if a.InForce(day("2026-02-28")) || !a.InForce(day("2026-03-01")) || !a.InForce(ends) || a.InForce(day("2027-01-01")) {
		t.Fatal("in force only from the issue date to the expiry, both included")
	}
}

func TestValidatePlatformSheet(t *testing.T) {
	ok := domain.PlatformSheet{PlatformName: "  ListerCore   Edu ", URL: "https://ceba.listercore.edu.pe", HostingKind: "hosting", TechnicalLeadCIP: "123456"}
	if f := validatePlatformSheet(&ok); len(f) > 0 {
		t.Fatalf("valid sheet: %v", f)
	}
	if ok.PlatformName != "ListerCore Edu" {
		t.Fatalf("spaces not collapsed: %q", ok.PlatformName)
	}
	for name, c := range map[string]struct {
		in    domain.PlatformSheet
		field string
	}{
		"url without scheme":   {domain.PlatformSheet{URL: "ceba.pe", HostingKind: "hosting"}, "url"},
		"unknown hosting":      {domain.PlatformSheet{HostingKind: "cloud"}, "hostingKind"},
		"other needs a detail": {domain.PlatformSheet{HostingKind: "other"}, "hostingOther"},
		"cip digits only":      {domain.PlatformSheet{HostingKind: "hosting", TechnicalLeadCIP: "CIP-12"}, "technicalLeadCip"},
	} {
		in := c.in
		if f := validatePlatformSheet(&in); f[c.field] == "" {
			t.Errorf("%s: want error on %s, got %v", name, c.field, f)
		}
	}
}

func TestGrantableRolesFollowTheInstitutionKind(t *testing.T) {
	has := func(kind, role string) bool {
		for _, r := range grantableRoles(kind) {
			if r == role {
				return true
			}
		}
		return false
	}
	if !has(domain.KindEBA, domain.RoleTechnicalLead) || has(domain.KindEBR, domain.RoleTechnicalLead) {
		t.Fatal("the Responsable técnico is a CEBA role")
	}
	if has(domain.KindEBA, "assistant") || !has(domain.KindEBE, RoleSpecialEd) || !has(domain.KindInstitute, "program_coordinator") {
		t.Fatal("roles must match the README role matrix")
	}
	for kind := range grantableStaffRoles {
		if has(kind, "student") || has(kind, "guardian") || has(kind, domain.RoleSuperadmin) {
			t.Fatalf("%s: families and the superadmin never get access here", kind)
		}
	}
}
