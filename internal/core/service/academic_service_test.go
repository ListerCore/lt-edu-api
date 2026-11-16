package service

import (
	"strings"
	"testing"
	"time"

	"api/internal/core/domain"
)

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func TestSplitTermsCoversThePeriodBackToBack(t *testing.T) {
	for _, scheme := range []string{domain.TermSchemeBimester, domain.TermSchemeTrimester, domain.TermSchemeSemester} {
		from, to := day("2026-03-09"), day("2026-12-18")
		terms := splitTerms(scheme, from, to)
		if len(terms) != termsPerScheme[scheme] {
			t.Fatalf("%s: %d terms", scheme, len(terms))
		}
		if !terms[0].StartsOn.Equal(from) || !terms[len(terms)-1].EndsOn.Equal(to) {
			t.Fatalf("%s: terms do not cover the period", scheme)
		}
		for i := 1; i < len(terms); i++ {
			if !terms[i].StartsOn.Equal(terms[i-1].EndsOn.AddDate(0, 0, 1)) {
				t.Fatalf("%s: gap or overlap before term %d", scheme, i+1)
			}
		}
	}
	if got := splitTerms(scheme("bimester"), day("2026-03-09"), day("2026-12-18"))[1].Name; got != "II bimestre" {
		t.Fatalf("name = %q", got)
	}
	if n := len(splitTerms(domain.TermSchemeNone, day("2026-03-09"), day("2026-12-18"))); n != 0 {
		t.Fatalf("none scheme gave %d terms", n)
	}
}

func scheme(s string) string { return s }

func TestCodeFrom(t *testing.T) {
	cases := map[[2]string]string{
		{"", "Ciclo Avanzado"}:       "ciclo-avanzado",
		{"", "San Marcos · Área A"}:  "san-marcos-area-a",
		{"", "1.° grado"}:            "1-grado",
		{"Mi Código", "ignorado"}:    "mi-codigo",
		{"", "Enfermería Técnica  "}: "enfermeria-tecnica",
		{"", "---"}:                  "",
	}
	for in, want := range cases {
		if got := codeFrom(in[0], in[1]); got != want {
			t.Errorf("codeFrom(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestProfilesFollowTheInstitutionKind(t *testing.T) {
	if p := profileFor(domain.KindCEPRE); p.hasGrades || !p.usesSeasons || p.periodKind != domain.PeriodCycle || p.singleActivePeriod {
		t.Fatal("cepre: areas without grades, seasonal cycles that may overlap")
	}
	if p := profileFor(domain.KindInstitute); p.scale != domain.ScaleVigesimal || p.passing == nil || *p.passing != 13 || !p.usesFormativeLevels {
		t.Fatal("institute: vigesimal scale passing at 13, formative levels")
	}
	if p := profileFor(domain.KindEBA); !p.usesAttentionModes || p.periodKind != domain.PeriodPromotional || p.singleActivePeriod {
		t.Fatal("eba: attention modes and overlapping promotional periods")
	}
	if p := profileFor(domain.KindEBR); p.termSchemes[0] != domain.TermSchemeBimester || !p.conduct {
		t.Fatal("ebr: bimesters by default and conduct mark")
	}
	for kind, p := range academicProfiles {
		if len(p.template) == 0 {
			t.Fatalf("%s has no template", kind)
		}
		for _, l := range p.template {
			if p.hasGrades != (len(l.Grades) > 0) {
				t.Fatalf("%s/%s: grades do not match the kind", kind, l.Code)
			}
			if codeFrom(l.Code, l.Name) != l.Code {
				t.Fatalf("%s/%s: template code is not normalized", kind, l.Code)
			}
		}
	}
}

// TestApplyProfile: the official data of the report card is trimmed and checked; nil keeps, "" clears the logo
func TestApplyProfile(t *testing.T) {
	str := func(s string) *string { return &s }
	next := domain.AcademicSettings{Dre: "DRE Lima", Logo: "data:image/png;base64,AAAA"}
	fields := map[string]string{}
	applyProfile(ProfileInput{Ugel: str("  UGEL Huamanga "), ModularCode: str("0999101"), Logo: str("")}, &next, fields)
	if len(fields) != 0 || next.Dre != "DRE Lima" || next.Ugel != "UGEL Huamanga" || next.ModularCode != "0999101" || next.Logo != "" {
		t.Fatalf("unexpected %+v %v", next, fields)
	}
	fields = map[string]string{}
	applyProfile(ProfileInput{ModularCode: str("12345"), Logo: str("data:text/html;base64,PHNjcmlwdD4=")}, &next, fields)
	if fields["modularCode"] == "" || fields["logo"] == "" {
		t.Fatalf("expected errors, got %v", fields)
	}
	fields = map[string]string{}
	applyProfile(ProfileInput{Logo: str("data:image/svg+xml;base64," + strings.Repeat("A", maxLogoLength))}, &next, fields)
	if fields["logo"] == "" {
		t.Fatal("a logo over 100 KB is refused")
	}
}

func TestValidateEBASectionProgramsAndDistanceVariants(t *testing.T) {
	advanced := domain.Level{AttentionModes: []string{"in_person", "blended", "distance"}}
	intermediate := domain.Level{AttentionModes: []string{"in_person", "blended"}}
	cases := []struct {
		name        string
		lvl         domain.Level
		in          domain.SectionInput
		wantField   string
		wantProgram string
		wantVariant string
	}{
		{"empty program is PEBAJA", advanced, domain.SectionInput{AttentionMode: "in_person"}, "", domain.EBAProgramPEBAJA, ""},
		{"distance defaults to virtual", advanced, domain.SectionInput{AttentionMode: "distance"}, "", domain.EBAProgramPEBAJA, domain.DistanceVirtual},
		{"itinerant distance", advanced, domain.SectionInput{AttentionMode: "distance", DistanceVariant: "itinerant"}, "", domain.EBAProgramPEBAJA, domain.DistanceItinerant},
		{"variant dropped off distance", advanced, domain.SectionInput{AttentionMode: "blended", DistanceVariant: "itinerant"}, "", domain.EBAProgramPEBAJA, ""},
		{"unknown variant", advanced, domain.SectionInput{AttentionMode: "distance", DistanceVariant: "radio"}, "distanceVariant", domain.EBAProgramPEBAJA, "radio"},
		{"literacy circle in person", intermediate, domain.SectionInput{AttentionMode: "in_person", Program: "literacy"}, "", domain.EBAProgramLiteracy, ""},
		{"literacy must be in person", intermediate, domain.SectionInput{AttentionMode: "blended", Program: "literacy"}, "attentionMode", domain.EBAProgramLiteracy, ""},
		{"no literacy in the advanced cycle", advanced, domain.SectionInput{AttentionMode: "in_person", Program: "literacy"}, "program", domain.EBAProgramLiteracy, ""},
		{"unknown program", advanced, domain.SectionInput{AttentionMode: "in_person", Program: "night"}, "program", "night", ""},
	}
	for _, c := range cases {
		fields := map[string]string{}
		in := c.in
		validateEBASection(c.lvl, &in, fields)
		if c.wantField == "" && len(fields) > 0 {
			t.Errorf("%s: unexpected errors %v", c.name, fields)
		}
		if c.wantField != "" && fields[c.wantField] == "" {
			t.Errorf("%s: want error on %s, got %v", c.name, c.wantField, fields)
		}
		if in.Program != c.wantProgram || in.DistanceVariant != c.wantVariant {
			t.Errorf("%s: program %q variant %q, want %q %q", c.name, in.Program, in.DistanceVariant, c.wantProgram, c.wantVariant)
		}
	}
}
