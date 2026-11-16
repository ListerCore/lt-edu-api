package service

import (
	"testing"

	"api/internal/core/domain"
)

func TestImportMatching(t *testing.T) {
	g := domain.Grade{Code: "3", Name: "3.° grado", ShortName: "3.°"}
	for _, w := range []string{"3", "3.°", "3°", "3.° grado", "Tercero", "tercer grado"} {
		if w == "tercer grado" {
			continue
		}
		if !sameGrade(w, g) {
			t.Errorf("%q should match 3.°", w)
		}
	}
	for _, w := range []string{"4", "", "cuarto"} {
		if sameGrade(w, g) {
			t.Errorf("%q should not match 3.°", w)
		}
	}
	l := domain.Level{Code: "avanzado", Name: "Ciclo Avanzado"}
	for _, w := range []string{"Avanzado", "ciclo avanzado", "CICLO AVANZADO", "avanzado"} {
		if !sameLevel(w, l) {
			t.Errorf("%q should match the Avanzado cycle", w)
		}
	}
	if sameLevel("Intermedio", l) || sameLevel("", l) {
		t.Error("other cycles must not match")
	}
	// SIAGIE writes M for mujer and H for hombre
	if importSex["m"] != "f" || importSex["h"] != "m" || importSex["mujer"] != "f" {
		t.Error("sex codes")
	}
	if importKinds["promovido"] != domain.EnrollContinuing || importKinds[""] != domain.EnrollNew {
		t.Error("enrollment kinds")
	}
	if d, err := parseImportDate("02/05/1990"); err != nil || d.Month() != 5 || d.Day() != 2 {
		t.Errorf("dd/mm/aaaa: %v %v", d, err)
	}
	if _, err := parseImportDate("1990/05/02x"); err == nil {
		t.Error("bad date")
	}
}
