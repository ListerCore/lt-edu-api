package service

import (
	"context"
	"errors"
	"regexp"
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
	repo port.InstitutionRepository
	auth *AuthService
	live *LiveService
	now  func() time.Time
}

func NewInstitutionService(repo port.InstitutionRepository, auth *AuthService, live *LiveService) *InstitutionService {
	return &InstitutionService{repo: repo, auth: auth, live: live, now: time.Now}
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
	s.changed(ctx, sc, "platform")
	return out, nil
}
