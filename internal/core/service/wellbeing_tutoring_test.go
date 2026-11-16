package service

import (
	"testing"

	"api/internal/core/domain"
)

func tutorScopeOf(role, staffID string, children ...string) wellScope {
	sc := wellScope{}
	sc.role, sc.staffID, sc.children = role, staffID, children
	return sc
}

func TestThreadSide(t *testing.T) {
	th := domain.Thread{StudentID: "ana", StaffID: "rosa"}
	cases := []struct {
		name string
		sc   wellScope
		side string
		ok   bool
	}{
		{"the tutor", tutorScopeOf("tutor", "rosa"), domain.SideStaff, true},
		{"another tutor", tutorScopeOf("tutor", "juan"), "", false},
		{"the student", tutorScopeOf("student", "", "ana"), domain.SideStudent, true},
		{"another student", tutorScopeOf("student", "", "luis"), "", false},
		{"a guardian", tutorScopeOf("guardian", "", "ana"), "", false},
		{"dirección reads", tutorScopeOf("director", ""), "", true},
		{"dirección who is the tutor writes", tutorScopeOf("director", "rosa"), domain.SideStaff, true},
	}
	for _, c := range cases {
		side, err := c.sc.threadSide(th)
		if side != c.side || (err == nil) != c.ok {
			t.Errorf("%s: %q %v", c.name, side, err)
		}
	}
}

func TestTutorsStudent(t *testing.T) {
	st := domain.TutorStudent{StudentID: "ana", StaffID: "rosa"}
	if !tutorScopeOf("tutor", "rosa").tutorsStudent(st) || tutorScopeOf("tutor", "juan").tutorsStudent(st) || !tutorScopeOf("academic_coordinator", "").tutorsStudent(st) {
		t.Fatal("tutor of the student")
	}
	if tutorScopeOf("tutor", "").tutorsStudent(domain.TutorStudent{StudentID: "eva"}) {
		t.Fatal("no staff record, no tutor")
	}
}

func TestValidLink(t *testing.T) {
	for v, want := range map[string]bool{"https://meet.google.com/abc-defg-hij": true, "http://jitsi.example/x": true, "meet.google.com/abc": false, "javascript:alert(1)": false, "https://": false} {
		if validLink(v) != want {
			t.Errorf("%s: want %v", v, want)
		}
	}
}
