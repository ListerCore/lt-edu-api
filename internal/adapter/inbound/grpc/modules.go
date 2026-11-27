package grpc

import (
	"context"
	"strings"

	gogrpc "google.golang.org/grpc"

	"api/internal/adapter/inbound/grpc/gen/apafav1"
	"api/internal/adapter/inbound/grpc/gen/classroomv1"
	"api/internal/adapter/inbound/grpc/gen/compliancev1"
	"api/internal/adapter/inbound/grpc/gen/examsv1"
	"api/internal/adapter/inbound/grpc/gen/financev1"
	"api/internal/adapter/inbound/grpc/gen/institutionv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// moduleOfService maps the gRPC service that serves each optional module (domain.OptionalModules) to its key
var moduleOfService = map[string]string{
	financev1.FinanceService_ServiceDesc.ServiceName:       "payments",
	classroomv1.ClassroomService_ServiceDesc.ServiceName:   "classroom",
	examsv1.ExamsService_ServiceDesc.ServiceName:           "mock-exams",
	compliancev1.ComplianceService_ServiceDesc.ServiceName: "compliance",
	apafav1.ApafaService_ServiceDesc.ServiceName:           "apafa",
}

type slugRequest interface{ GetInstitutionSlug() string }

// moduleInterceptor refuses the calls to an optional module the caller's institution turned off. It runs after the
// client resolver, so the session lookup it needs is shared with the service through the request memo
func moduleInterceptor(institution *service.InstitutionService) gogrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *gogrpc.UnaryServerInfo, handler gogrpc.UnaryHandler) (any, error) {
		serviceName, _, _ := strings.Cut(strings.TrimPrefix(info.FullMethod, "/"), "/")
		module, guarded := moduleOfService[serviceName]
		if guarded && institution != nil {
			slug := ""
			if r, ok := req.(slugRequest); ok {
				slug = r.GetInstitutionSlug()
			}
			if err := institution.ModuleAllowed(ctx, bearer(ctx), slug, module); err != nil {
				return nil, fromDomain(err)
			}
		}
		return handler(ctx, req)
	}
}

func toOptionalModule(m domain.ModuleState) *institutionv1.Module {
	return &institutionv1.Module{Key: m.Key, Name: m.Name, Enabled: m.Enabled}
}

func (s *institutionServer) ListModules(ctx context.Context, req *institutionv1.ListModulesRequest) (*institutionv1.ListModulesResponse, error) {
	modules, canManage, err := s.institution.Modules(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListModules", err)
	}
	out := &institutionv1.ListModulesResponse{CanManage: canManage}
	for _, m := range modules {
		out.Modules = append(out.Modules, toOptionalModule(m))
	}
	return out, nil
}

func (s *institutionServer) SetModule(ctx context.Context, req *institutionv1.SetModuleRequest) (*institutionv1.SetModuleResponse, error) {
	m, err := s.institution.SetModule(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetKey(), req.GetEnabled())
	if err != nil {
		return nil, s.fail("SetModule", err)
	}
	return &institutionv1.SetModuleResponse{Module: toOptionalModule(m)}, nil
}
