package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
	"api/internal/core/port"
)

var (
	staffManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	staffViewers  = map[string]bool{"program_coordinator": true, "special_ed_specialist": true, "secretary": true, "teacher": true, "tutor": true, "assistant": true, "psychologist": true, "treasurer": true}
	// Roles the panel can give to staff (never superadmin, student or guardian)
	staffRoles = map[string]bool{"program_coordinator": true, "special_ed_specialist": true, "director": true, "academic_coordinator": true, "secretary": true, "teacher": true, "tutor": true, "assistant": true, "psychologist": true, "treasurer": true}
)

// StaffService runs the staff of each institution: their record, whether they teach and their panel roles
type StaffService struct {
	repo     port.StaffRepository
	teaching port.TeachingRepository
	people   port.PeopleRepository
	academic *AcademicService
	now      func() time.Time
}

func NewStaffService(repo port.StaffRepository, teaching port.TeachingRepository, people port.PeopleRepository, academic *AcademicService) *StaffService {
	return &StaffService{repo: repo, teaching: teaching, people: people, academic: academic, now: time.Now}
}

func (s *StaffService) scope(ctx context.Context, token, slug string, write bool) (academicScope, error) {
	sc, err := resolveInstitution(ctx, s.academic.auth, s.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return academicScope{}, err
	}
	switch {
	case staffManagers[sc.role]:
	case staffViewers[sc.role]:
		if write {
			return academicScope{}, domain.ErrStaffReadOnly
		}
	default:
		return academicScope{}, domain.ErrStaffForbidden
	}
	return academicScope{institutionScope: sc, profile: profileFor(sc.institution.Kind)}, nil
}

func (s *StaffService) changed(ctx context.Context, sc academicScope) {
	s.academic.live.Notify(ctx, EventPeopleChanged, map[string]string{"institution": sc.institution.Slug, "area": "staff"})
}

// StaffPage is a page of the directory and whether the person may change it
type StaffPage struct {
	Staff     []domain.Staff
	Total     int
	CanManage bool
	// Only dirección (and the superadmin) give panel roles
	CanGrant bool
}

func (s *StaffService) List(ctx context.Context, token, slug string, f domain.StaffFilter) (StaffPage, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return StaffPage{}, err
	}
	f.InstitutionID = sc.institution.ID
	list, total, err := s.repo.StaffList(ctx, f)
	return StaffPage{Staff: list, Total: total, CanManage: staffManagers[sc.role], CanGrant: canGrant(sc.role)}, err
}

func canGrant(role string) bool { return role == domain.RoleSuperadmin || role == "director" }

// StaffDetail is one person with their teaching load in running periods
type StaffDetail struct {
	Staff       domain.Staff
	Assignments []domain.Assignment
	CanManage   bool
	CanGrant    bool
}

func (s *StaffService) Get(ctx context.Context, token, slug, id string) (StaffDetail, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return StaffDetail{}, err
	}
	return s.detail(ctx, sc, id)
}

func (s *StaffService) detail(ctx context.Context, sc academicScope, id string) (StaffDetail, error) {
	st, err := s.repo.Staff(ctx, sc.institution.ID, id)
	if err != nil {
		return StaffDetail{}, err
	}
	load, err := s.teaching.StaffAssignments(ctx, sc.institution.ID, id)
	if err != nil {
		return StaffDetail{}, err
	}
	return StaffDetail{Staff: st, Assignments: load, CanManage: staffManagers[sc.role], CanGrant: canGrant(sc.role)}, nil
}

func (s *StaffService) validate(in *domain.StaffInput) error {
	fields := map[string]string{}
	in.GivenNames, in.Surnames, in.Position = strings.TrimSpace(in.GivenNames), strings.TrimSpace(in.Surnames), strings.TrimSpace(in.Position)
	in.Code, in.Specialty, in.Notes = strings.TrimSpace(in.Code), strings.TrimSpace(in.Specialty), strings.TrimSpace(in.Notes)
	if n := utf8.RuneCountInString(in.GivenNames); n < 2 || n > 80 {
		fields["givenNames"] = "Escribe los nombres."
	}
	if n := utf8.RuneCountInString(in.Surnames); n < 2 || n > 120 {
		fields["surnames"] = "Escribe los apellidos."
	}
	if n := utf8.RuneCountInString(in.Position); n < 2 || n > 80 {
		fields["position"] = "Escribe el cargo."
	}
	if utf8.RuneCountInString(in.Specialty) > 120 {
		fields["specialty"] = "Máximo 120 caracteres."
	}
	if in.Code != "" && !studentCodePatern.MatchString(in.Code) {
		fields["code"] = "Usa hasta 20 letras, números o guiones."
	}
	validateDocument(fields, &in.DocumentType, &in.DocumentNumber)
	validateContact(fields, &in.Email, &in.Phone)
	if in.MaxWeeklyHours != nil && (*in.MaxWeeklyHours < 1 || *in.MaxWeeklyHours > 60) {
		fields["maxWeeklyHours"] = "Entre 1 y 60 horas."
	}
	if in.HiredOn != nil && in.HiredOn.After(s.now().AddDate(1, 0, 0)) {
		fields["hiredOn"] = "Revisa la fecha de ingreso."
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "inactive" {
		fields["status"] = "Elige un estado válido."
	}
	if utf8.RuneCountInString(in.Notes) > 1000 {
		fields["notes"] = "Máximo 1000 caracteres."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

func (s *StaffService) Create(ctx context.Context, token, slug string, in domain.StaffInput) (domain.Staff, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Staff{}, err
	}
	if err := s.validate(&in); err != nil {
		return domain.Staff{}, err
	}
	st, err := s.repo.CreateStaff(ctx, sc.institution.ID, in)
	if err == nil {
		s.changed(ctx, sc)
	}
	return st, err
}

func (s *StaffService) Update(ctx context.Context, token, slug, id string, in domain.StaffInput) (domain.Staff, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Staff{}, err
	}
	if err := s.validate(&in); err != nil {
		return domain.Staff{}, err
	}
	if in.Code == "" {
		current, err := s.repo.Staff(ctx, sc.institution.ID, id)
		if err != nil {
			return domain.Staff{}, err
		}
		in.Code = current.Code
	}
	st, err := s.repo.UpdateStaff(ctx, sc.institution.ID, id, in)
	if err == nil {
		s.changed(ctx, sc)
	}
	return st, err
}

func (s *StaffService) Delete(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteStaff(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc)
	return nil
}

// GrantRole gives the person a panel role in the institution with their email (dirección only)
func (s *StaffService) GrantRole(ctx context.Context, token, slug, id, role string) (StaffDetail, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return StaffDetail{}, err
	}
	if !canGrant(sc.role) {
		return StaffDetail{}, &domain.Error{Kind: domain.KindForbidden, Code: "grant_forbidden", Message: "Solo dirección puede dar roles en el panel."}
	}
	if !staffRoles[role] {
		return StaffDetail{}, domain.Validation(map[string]string{"role": "Elige un rol válido."})
	}
	st, err := s.repo.Staff(ctx, sc.institution.ID, id)
	if err != nil {
		return StaffDetail{}, err
	}
	if st.Email == "" {
		return StaffDetail{}, domain.ErrNoEmailForAccess
	}
	for _, r := range st.Roles {
		if r == role {
			return StaffDetail{}, domain.ErrAlreadyHasAccess
		}
	}
	if _, err := s.people.GrantAccess(ctx, sc.institution.ID, st.Email, role, sc.userID); err != nil {
		return StaffDetail{}, err
	}
	s.changed(ctx, sc)
	return s.detail(ctx, sc, id)
}
