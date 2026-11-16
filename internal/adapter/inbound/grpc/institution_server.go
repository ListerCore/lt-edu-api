package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/institutionv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// institutionServer exposes service.InstitutionService (the institution file) to joseph-app's server
type institutionServer struct {
	institutionv1.UnimplementedInstitutionServiceServer
	institution *service.InstitutionService
	logger      zerolog.Logger
}

func (s *institutionServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("institution grpc call failed")
	}
	return fromDomain(err)
}

func toAuthorization(a domain.Authorization, today time.Time) *institutionv1.Authorization {
	return &institutionv1.Authorization{Id: a.ID, Kind: a.Kind, LevelId: a.LevelID, LevelName: a.LevelName, AttentionMode: a.AttentionMode,
		Resolution: a.Resolution, IssuedBy: a.IssuedBy, IssuedOn: formatDate(a.IssuedOn), ExpiresOn: datePtr(a.ExpiresOn), FileNumber: a.FileNumber,
		Notes: a.Notes, InForce: a.InForce(today), Files: int32(a.Files)}
}

func toPlatformSheet(p domain.PlatformSheet) *institutionv1.PlatformSheet {
	updated := ""
	if !p.UpdatedAt.IsZero() {
		updated = p.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return &institutionv1.PlatformSheet{PlatformName: p.PlatformName, Url: p.URL, Technology: p.Technology, Administrator: p.Administrator,
		AccessProfiles: p.AccessProfiles, HostingKind: p.HostingKind, HostingOther: p.HostingOther, Provider: p.Provider, Instance: p.Instance,
		ContractReference: p.ContractReference, ServiceStatus: p.ServiceStatus, ValidUntil: datePtr(p.ValidUntil), TechnicalLead: p.TechnicalLead,
		TechnicalLeadSpecialty: p.TechnicalLeadSpecialty, TechnicalLeadCip: p.TechnicalLeadCIP, UpdatedByName: p.UpdatedByName,
		UpdatedAt: updated, Files: int32(p.Files)}
}

func (s *institutionServer) GetInstitutionFile(ctx context.Context, req *institutionv1.GetInstitutionFileRequest) (*institutionv1.GetInstitutionFileResponse, error) {
	f, err := s.institution.File(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetInstitutionFile", err)
	}
	today := time.Now()
	out := &institutionv1.GetInstitutionFileResponse{InstitutionId: f.Institution.ID, InstitutionName: f.Institution.Name, InstitutionKind: f.Institution.Kind,
		Platform: toPlatformSheet(f.Platform), CanManageAuthorizations: f.CanManageAuthorization, CanEditPlatform: f.CanEditPlatform}
	for _, a := range f.Authorizations {
		out.Authorizations = append(out.Authorizations, toAuthorization(a, today))
	}
	return out, nil
}

func (s *institutionServer) SaveAuthorization(ctx context.Context, req *institutionv1.SaveAuthorizationRequest) (*institutionv1.SaveAuthorizationResponse, error) {
	in := req.GetAuthorization()
	dates, err := parseDates(map[string]string{"issuedOn": in.GetIssuedOn()})
	if err != nil {
		return nil, s.fail("SaveAuthorization", err)
	}
	expires, err := optionalDate("expiresOn", in.GetExpiresOn())
	if err != nil {
		return nil, s.fail("SaveAuthorization", err)
	}
	a, err := s.institution.SaveAuthorization(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Authorization{ID: in.GetId(), Kind: in.GetKind(),
		LevelID: in.GetLevelId(), AttentionMode: in.GetAttentionMode(), Resolution: in.GetResolution(), IssuedBy: in.GetIssuedBy(),
		IssuedOn: dates["issuedOn"], ExpiresOn: expires, FileNumber: in.GetFileNumber(), Notes: in.GetNotes()})
	if err != nil {
		return nil, s.fail("SaveAuthorization", err)
	}
	return &institutionv1.SaveAuthorizationResponse{Authorization: toAuthorization(a, time.Now())}, nil
}

func (s *institutionServer) DeleteAuthorization(ctx context.Context, req *institutionv1.DeleteAuthorizationRequest) (*institutionv1.DeleteAuthorizationResponse, error) {
	if err := s.institution.DeleteAuthorization(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteAuthorization", err)
	}
	return &institutionv1.DeleteAuthorizationResponse{}, nil
}

func (s *institutionServer) SavePlatformSheet(ctx context.Context, req *institutionv1.SavePlatformSheetRequest) (*institutionv1.SavePlatformSheetResponse, error) {
	in := req.GetPlatform()
	valid, err := optionalDate("validUntil", in.GetValidUntil())
	if err != nil {
		return nil, s.fail("SavePlatformSheet", err)
	}
	p, err := s.institution.SavePlatformSheet(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.PlatformSheet{PlatformName: in.GetPlatformName(),
		URL: in.GetUrl(), Technology: in.GetTechnology(), Administrator: in.GetAdministrator(), AccessProfiles: in.GetAccessProfiles(),
		HostingKind: in.GetHostingKind(), HostingOther: in.GetHostingOther(), Provider: in.GetProvider(), Instance: in.GetInstance(),
		ContractReference: in.GetContractReference(), ServiceStatus: in.GetServiceStatus(), ValidUntil: valid, TechnicalLead: in.GetTechnicalLead(),
		TechnicalLeadSpecialty: in.GetTechnicalLeadSpecialty(), TechnicalLeadCIP: in.GetTechnicalLeadCip()})
	if err != nil {
		return nil, s.fail("SavePlatformSheet", err)
	}
	return &institutionv1.SavePlatformSheetResponse{Platform: toPlatformSheet(p)}, nil
}
