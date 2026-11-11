package service

import (
	"context"
	_ "embed"
	"encoding/base64"

	"api/internal/core/domain"
)

// The demo logo (the platform's, scaled down) used by every demo institution on its report cards
//
//go:embed seeddata/demo-logo.png
var demoLogo []byte

// ProfileStore reads and writes the institution settings the demo profile goes into
type ProfileStore interface {
	InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error)
	Settings(ctx context.Context, institutionID string) (domain.AcademicSettings, error)
	SaveSettings(ctx context.Context, institutionID string, s domain.AcademicSettings) (domain.AcademicSettings, error)
}

// demoProfiles: regional office, UGEL and a made-up modular code (9999xxx, not a real school) per demo institution.
// Academies are not in the MINEDU register, and institutes answer to the DRE without a UGEL
var demoProfiles = map[string][3]string{
	"demo-ebr":       {"DRE Ayacucho", "UGEL Huamanga", "9999101"},
	"demo-eba":       {"DRE Ayacucho", "UGEL Huamanga", "9999102"},
	"demo-ebe":       {"DRE Ayacucho", "UGEL Huamanga", "9999103"},
	"demo-cepre":     {"", "", ""},
	"demo-institute": {"DRE Ayacucho", "", "9999105"},
}

// SeedDemoProfiles fills the official header of the demo report cards
func SeedDemoProfiles(ctx context.Context, repo ProfileStore) (int, error) {
	logo := "data:image/png;base64," + base64.StdEncoding.EncodeToString(demoLogo)
	n := 0
	for slug, p := range demoProfiles {
		inst, err := repo.InstitutionBySlug(ctx, slug)
		if err != nil {
			return n, err
		}
		s, err := repo.Settings(ctx, inst.ID)
		if err != nil {
			return n, err
		}
		s.Dre, s.Ugel, s.ModularCode, s.Logo = p[0], p[1], p[2], logo
		if _, err := repo.SaveSettings(ctx, inst.ID, s); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
