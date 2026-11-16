package service

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// EventInstitutionChanged: {institution, area (authorizations | platform)}
const EventInstitutionChanged = "institution.changed"

var (
	// Dirección, coordinación, secretaría and the Responsable técnico read the institution file
	institutionFileReaders = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true, "secretary": true, domain.RoleTechnicalLead: true}
	// Only dirección records the resolutions that authorize the institution
	authorizationManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true}
	// The platform sheet is the Responsable técnico's, with dirección
	platformSheetEditors = map[string]bool{domain.RoleSuperadmin: true, "director": true, domain.RoleTechnicalLead: true}

	authorizationKinds = map[string]bool{domain.AuthorizationCreation: true, domain.AuthorizationAttentionMode: true, domain.AuthorizationExpansion: true, domain.AuthorizationOther: true}
	hostingKinds       = map[string]bool{domain.HostingService: true, domain.HostingOwnServer: true, domain.HostingOther: true}
	platformURLPattern = regexp.MustCompile(`^https?://[^\s<>"]+$`)
	cipPattern         = regexp.MustCompile(`^[0-9]{0,20}$`)
)

// InstitutionService keeps the institution file a CEBA presents to the DRE: the resolutions that authorize it
// (creation, each form of attention per cycle) and the technical sheet of its e-learning platform (informe 6.2.1)
type InstitutionService struct {
	repo  port.InstitutionRepository
	auth  *AuthService
	live  *LiveService
	audit *AuditService
	now   func() time.Time
}

func NewInstitutionService(repo port.InstitutionRepository, auth *AuthService, live *LiveService, audit *AuditService) *InstitutionService {
	return &InstitutionService{repo: repo, auth: auth, live: live, audit: audit, now: time.Now}
}

// Spanish names for the audit log
var (
	authorizationKindLabels = map[string]string{domain.AuthorizationCreation: "creación", domain.AuthorizationAttentionMode: "forma de atención",
		domain.AuthorizationExpansion: "ampliación", domain.AuthorizationOther: "otra"}
	attentionModeLabels = map[string]string{"in_person": "presencial", "blended": "semipresencial", "distance": "a distancia"}
)

// authorizationLabel: "RD 0123-2026 (forma de atención: a distancia)"
func authorizationLabel(a domain.Authorization) string {
	detail := authorizationKindLabels[a.Kind]
	if a.Kind == domain.AuthorizationAttentionMode {
		detail += ": " + attentionModeLabels[a.AttentionMode]
		if a.LevelName != "" {
			detail += ", " + a.LevelName
		}
	}
	return a.Resolution + " (" + detail + ")"
}

type institutionFileScope struct {
	institutionScope
	userName string
}

func (s *InstitutionService) scope(ctx context.Context, token, slug string) (institutionFileScope, error) {
	sc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return institutionFileScope{}, err
	}
	if !institutionFileReaders[sc.role] {
		return institutionFileScope{}, domain.ErrInstitutionFileForbidden
	}
	me, err := s.auth.Me(ctx, token)
	if err != nil {
		return institutionFileScope{}, err
	}
	return institutionFileScope{institutionScope: sc, userName: uploaderName(me.User)}, nil
}

func (s *InstitutionService) changed(ctx context.Context, sc institutionFileScope, area string) {
	s.live.Notify(ctx, EventInstitutionChanged, map[string]string{"institution": sc.institution.Slug, "area": area})
}

// InstitutionFile is the Configuración › Institución page: the authorizations, the platform sheet and what the
// person may change
type InstitutionFile struct {
	Institution            domain.Institution
	Authorizations         []domain.Authorization
	Platform               domain.PlatformSheet
	CanManageAuthorization bool
	CanEditPlatform        bool
}

func (s *InstitutionService) File(ctx context.Context, token, slug string) (InstitutionFile, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return InstitutionFile{}, err
	}
	auths, err := s.repo.Authorizations(ctx, sc.institution.ID)
	if err != nil {
		return InstitutionFile{}, err
	}
	platform, err := s.repo.PlatformSheet(ctx, sc.institution.ID)
	if err != nil {
		return InstitutionFile{}, err
	}
	return InstitutionFile{Institution: sc.institution, Authorizations: auths, Platform: platform,
		CanManageAuthorization: authorizationManagers[sc.role], CanEditPlatform: platformSheetEditors[sc.role]}, nil
}

// ---------- authorizations ----------

// validateAuthorization trims the fields and checks them. Only EBA authorizes forms of attention per cycle
func validateAuthorization(kind string, a *domain.Authorization) map[string]string {
	fields := map[string]string{}
	a.Resolution = strings.Join(strings.Fields(a.Resolution), " ")
	a.IssuedBy = strings.TrimSpace(a.IssuedBy)
	a.FileNumber = strings.TrimSpace(a.FileNumber)
	a.Notes = strings.TrimSpace(a.Notes)
	if !authorizationKinds[a.Kind] {
		fields["kind"] = "Elige el tipo de autorización."
	}
	if a.Kind == domain.AuthorizationAttentionMode {
		if kind != domain.KindEBA {
			fields["kind"] = "Las formas de atención solo se autorizan en un CEBA."
		} else if !validModes[a.AttentionMode] {
			fields["attentionMode"] = "Elige la forma de atención autorizada."
		}
	} else {
		a.AttentionMode, a.LevelID = "", ""
	}
	if n := utf8.RuneCountInString(a.Resolution); n < 3 || n > 120 {
		fields["resolution"] = "Escribe el número de la resolución (máximo 120 caracteres)."
	}
	if utf8.RuneCountInString(a.IssuedBy) > 160 {
		fields["issuedBy"] = "Máximo 160 caracteres."
	}
	if a.IssuedOn.IsZero() {
		fields["issuedOn"] = "Indica la fecha de la resolución."
	}
	if a.ExpiresOn != nil && !a.IssuedOn.IsZero() && a.ExpiresOn.Before(a.IssuedOn) {
		fields["expiresOn"] = "El vencimiento no puede ser anterior a la resolución."
	}
	if utf8.RuneCountInString(a.FileNumber) > 40 {
		fields["fileNumber"] = "Máximo 40 caracteres."
	}
	if utf8.RuneCountInString(a.Notes) > 500 {
		fields["notes"] = "Máximo 500 caracteres."
	}
	return fields
}

// SaveAuthorization creates (empty id) or edits one resolution of the institution
func (s *InstitutionService) SaveAuthorization(ctx context.Context, token, slug string, in domain.Authorization) (domain.Authorization, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Authorization{}, err
	}
	if !authorizationManagers[sc.role] {
		return domain.Authorization{}, domain.ErrInstitutionFileReadOnly
	}
	in.InstitutionID = sc.institution.ID
	if fields := validateAuthorization(sc.institution.Kind, &in); len(fields) > 0 {
		return domain.Authorization{}, domain.Validation(fields)
	}
	out, err := s.repo.SaveAuthorization(ctx, in)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) && in.ID == "" {
			return domain.Authorization{}, domain.Validation(map[string]string{"levelId": "Elige uno de los ciclos de la institución."})
		}
		return domain.Authorization{}, err
	}
	action, verb := domain.AuditUpdate, "Editó la autorización "
	if in.ID == "" {
		action, verb = domain.AuditCreate, "Registró la autorización "
	}
	s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "institution", Action: action, Entity: "authorization", EntityID: out.ID,
		Summary: verb + authorizationLabel(out)})
	s.changed(ctx, sc, "authorizations")
	return out, nil
}

func (s *InstitutionService) DeleteAuthorization(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	if !authorizationManagers[sc.role] {
		return domain.ErrInstitutionFileReadOnly
	}
	current, err := s.repo.Authorization(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if current.Files > 0 {
		return domain.ErrAuthorizationHasFiles
	}
	if err := s.repo.DeleteAuthorization(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "institution", Action: domain.AuditDelete, Entity: "authorization", EntityID: id,
		Summary: "Eliminó la autorización " + authorizationLabel(current)})
	s.changed(ctx, sc, "authorizations")
	return nil
}

// ---------- platform sheet ----------

func validatePlatformSheet(p *domain.PlatformSheet) map[string]string {
	fields := map[string]string{}
	limits := []struct {
		field string
		value *string
		max   int
	}{
		{"platformName", &p.PlatformName, 120}, {"url", &p.URL, 300}, {"technology", &p.Technology, 200}, {"administrator", &p.Administrator, 160},
		{"accessProfiles", &p.AccessProfiles, 300}, {"hostingOther", &p.HostingOther, 120}, {"provider", &p.Provider, 160}, {"instance", &p.Instance, 160},
		{"contractReference", &p.ContractReference, 160}, {"serviceStatus", &p.ServiceStatus, 160}, {"technicalLead", &p.TechnicalLead, 160},
		{"technicalLeadSpecialty", &p.TechnicalLeadSpecialty, 160}, {"technicalLeadCip", &p.TechnicalLeadCIP, 20},
	}
	for _, l := range limits {
		*l.value = strings.Join(strings.Fields(*l.value), " ")
		if utf8.RuneCountInString(*l.value) > l.max {
			fields[l.field] = "Máximo " + itoa(l.max) + " caracteres."
		}
	}
	if p.URL != "" && !platformURLPattern.MatchString(p.URL) {
		fields["url"] = "Escribe la dirección completa, con https://."
	}
	if !hostingKinds[p.HostingKind] {
		fields["hostingKind"] = "Elige el tipo de alojamiento."
	}
	if p.HostingKind == domain.HostingOther && p.HostingOther == "" {
		fields["hostingOther"] = "Describe el alojamiento."
	}
	if p.HostingKind != domain.HostingOther {
		p.HostingOther = ""
	}
	if !cipPattern.MatchString(p.TechnicalLeadCIP) {
		fields["technicalLeadCip"] = "Solo números."
	}
	return fields
}

func (s *InstitutionService) SavePlatformSheet(ctx context.Context, token, slug string, in domain.PlatformSheet) (domain.PlatformSheet, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.PlatformSheet{}, err
	}
	if !platformSheetEditors[sc.role] {
		return domain.PlatformSheet{}, domain.ErrPlatformSheetReadOnly
	}
	if fields := validatePlatformSheet(&in); len(fields) > 0 {
		return domain.PlatformSheet{}, domain.Validation(fields)
	}
	in.UpdatedByName = sc.userName
	out, err := s.repo.SavePlatformSheet(ctx, sc.institution.ID, in)
	if err != nil {
		return domain.PlatformSheet{}, err
	}
	s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "institution", Action: domain.AuditUpdate, Entity: "platform_sheet", EntityID: sc.institution.ID,
		Summary: "Actualizó la ficha de la plataforma"})
	s.changed(ctx, sc, "platform")
	return out, nil
}

// ---------- members ----------

// membersManagers: only dirección (and the platform superadmin) gives and takes the staff's access
var membersManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true}

// grantableStaffRoles are the accesses Configuración › Usuarios hands out, per institution kind. Students and guardians
// get theirs from enrollment, and teachers can also come from the staff module (same invitations underneath)
var grantableStaffRoles = map[string][]string{
	domain.KindEBR:       {"director", "academic_coordinator", "secretary", "teacher", "tutor", "assistant", "psychologist", "treasurer"},
	domain.KindEBA:       {"director", "academic_coordinator", "secretary", "teacher", "tutor", "psychologist", "treasurer", domain.RoleTechnicalLead},
	domain.KindEBE:       {"director", "academic_coordinator", "secretary", "teacher", "assistant", "psychologist", "treasurer", RoleSpecialEd},
	domain.KindCEPRE:     {"director", "academic_coordinator", "secretary", "teacher", "tutor", "treasurer"},
	domain.KindInstitute: {"director", "academic_coordinator", "program_coordinator", "secretary", "teacher", "tutor", "psychologist", "treasurer"},
}

// grantableRoles is grantableStaffRoles of the kind (EBR's when unknown)
func grantableRoles(kind string) []string {
	if roles, ok := grantableStaffRoles[kind]; ok {
		return roles
	}
	return grantableStaffRoles[domain.KindEBR]
}

// InstitutionMembers is the Usuarios page: the staff's accesses and the roles that can be given here
type InstitutionMembers struct {
	Members        []domain.Member
	GrantableRoles []string
	SelfUserID     string
}

func (s *InstitutionService) membersScope(ctx context.Context, token, slug string) (institutionFileScope, error) {
	sc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return institutionFileScope{}, err
	}
	if !membersManagers[sc.role] {
		return institutionFileScope{}, domain.ErrMembersForbidden
	}
	return institutionFileScope{institutionScope: sc}, nil
}

func (s *InstitutionService) Members(ctx context.Context, token, slug string) (InstitutionMembers, error) {
	sc, err := s.membersScope(ctx, token, slug)
	if err != nil {
		return InstitutionMembers{}, err
	}
	roles := grantableRoles(sc.institution.Kind)
	members, err := s.repo.Members(ctx, sc.institution.ID, roles)
	if err != nil {
		return InstitutionMembers{}, err
	}
	return InstitutionMembers{Members: members, GrantableRoles: roles, SelfUserID: sc.userID}, nil
}

// GrantMember gives a staff role to an email: at once when the account exists, or as an invitation the person
// accepts by signing up with that email (the platform never lets them pick a role). pending tells which
func (s *InstitutionService) GrantMember(ctx context.Context, token, slug, email, roleCode string) (bool, error) {
	sc, err := s.membersScope(ctx, token, slug)
	if err != nil {
		return false, err
	}
	email = normalizeEmail(email)
	fields := map[string]string{}
	if !validEmail(email) {
		fields["email"] = "Ingresa un correo válido."
	}
	if !slices.Contains(grantableRoles(sc.institution.Kind), roleCode) {
		fields["role"] = domain.ErrRoleNotGrantable.Message
	}
	if len(fields) > 0 {
		return false, domain.Validation(fields)
	}
	pending, err := s.repo.GrantRole(ctx, sc.institution.ID, email, roleCode, sc.userID)
	if err != nil {
		return false, err
	}
	summary := "Asignó el rol " + roleCode + " a " + email
	if pending {
		summary = "Invitó a " + email + " con el rol " + roleCode
	}
	s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "users", Action: domain.AuditGrant, Entity: "member", EntityID: email, Summary: summary,
		Changes: map[string]any{"role": roleCode, "pending": pending}})
	s.changed(ctx, sc, "members")
	return pending, nil
}

// RevokeMember takes a staff role away, or cancels the invitation. Nobody removes their own access, and the
// institution always keeps one director
func (s *InstitutionService) RevokeMember(ctx context.Context, token, slug, id string) error {
	sc, err := s.membersScope(ctx, token, slug)
	if err != nil {
		return err
	}
	m, err := s.repo.Member(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if !slices.Contains(grantableRoles(sc.institution.Kind), m.RoleCode) {
		return domain.ErrRoleNotGrantable
	}
	if !m.Pending && m.UserID == sc.userID {
		return domain.ErrRevokeOwnAccess
	}
	if !m.Pending && m.RoleCode == "director" {
		n, err := s.repo.CountRole(ctx, sc.institution.ID, "director")
		if err != nil {
			return err
		}
		if n <= 1 {
			return domain.ErrLastDirector
		}
	}
	if err := s.repo.RevokeMember(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	who := m.Email
	if m.FullName != "" {
		who = m.FullName + " (" + m.Email + ")"
	}
	summary := "Quitó el rol " + m.RoleCode + " a " + who
	if m.Pending {
		summary = "Canceló la invitación de " + m.Email + " como " + m.RoleCode
	}
	s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "users", Action: domain.AuditRevoke, Entity: "member", EntityID: id, Summary: summary,
		Changes: map[string]any{"role": m.RoleCode, "pending": m.Pending}})
	s.changed(ctx, sc, "members")
	return nil
}
