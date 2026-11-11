// Package grpc serves joseph-api over gRPC for joseph-app's server. It runs on its own port next to the HTTP API,
// over plaintext HTTP/2: like the HTTP API it must only be reachable on the internal network
package grpc

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/rs/zerolog"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"api/internal/adapter/inbound/grpc/gen/academicv1"
	"api/internal/adapter/inbound/grpc/gen/announcementsv1"
	"api/internal/adapter/inbound/grpc/gen/attendancev1"
	"api/internal/adapter/inbound/grpc/gen/peoplev1"
	"api/internal/adapter/inbound/grpc/gen/systemv1"
	"api/internal/adapter/inbound/grpc/gen/teachingv1"
	"api/internal/core/service"
)

type Config struct {
	// Proxies allowed to set x-forwarded-for besides loopback and private ranges
	TrustedProxies []string
	// Development exposes server reflection (grpcurl, Postman)
	Development bool
}

// Services are the core services exposed over gRPC; add a field per new service
type Services struct {
	Health        *service.HealthService
	Announcements *service.AnnouncementService
	Academic      *service.AcademicService
	Curricula     *service.CurriculumService
	People        *service.PeopleService
	Staff         *service.StaffService
	Teaching      *service.TeachingService
	Attendance    *service.AttendanceService
}

// limits brakes methods per client IP. Register a method here when it takes a password or costs a lot, e.g.
// foov1.FooService_Login_FullMethodName: newRateLimiter(10, time.Minute)
var limits = map[string]*rateLimiter{}

// New builds the server with every service registered; the caller owns Serve and GracefulStop
func New(services Services, cfg Config, logger zerolog.Logger) *gogrpc.Server {
	clients := newClientResolver(cfg.TrustedProxies, logger)

	server := gogrpc.NewServer(
		gogrpc.ChainUnaryInterceptor(
			recoverInterceptor(logger),
			clients.interceptor,
			logInterceptor(logger),
			rateLimitInterceptor(limits),
		),
		gogrpc.ChainStreamInterceptor(
			recoverStreamInterceptor(logger),
			clients.streamInterceptor,
		),
	)

	healthServer := health.NewServer()
	systemv1.RegisterSystemServiceServer(server, &systemServer{health: services.Health, logger: logger})
	healthServer.SetServingStatus(systemv1.SystemService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	announcementsv1.RegisterAnnouncementServiceServer(server, &announcementServer{announcements: services.Announcements, logger: logger})
	healthServer.SetServingStatus(announcementsv1.AnnouncementService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	academicv1.RegisterAcademicServiceServer(server, &academicServer{academic: services.Academic, logger: logger})
	healthServer.SetServingStatus(academicv1.AcademicService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	academicv1.RegisterCurriculumServiceServer(server, &curriculumServer{curricula: services.Curricula, logger: logger})
	healthServer.SetServingStatus(academicv1.CurriculumService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	peoplev1.RegisterPeopleServiceServer(server, &peopleServer{people: services.People, logger: logger})
	healthServer.SetServingStatus(peoplev1.PeopleService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	teachingv1.RegisterStaffServiceServer(server, &staffServer{staff: services.Staff, logger: logger})
	healthServer.SetServingStatus(teachingv1.StaffService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	teachingv1.RegisterTeachingServiceServer(server, &teachingServer{teaching: services.Teaching, logger: logger})
	healthServer.SetServingStatus(teachingv1.TeachingService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	attendancev1.RegisterAttendanceServiceServer(server, &attendanceServer{attendance: services.Attendance, logger: logger})
	healthServer.SetServingStatus(attendancev1.AttendanceService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)

	if cfg.Development {
		reflection.Register(server)
	}
	return server
}

func recoverInterceptor(logger zerolog.Logger) gogrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *gogrpc.UnaryServerInfo, handler gogrpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error().Interface("panic", r).Bytes("stack", debug.Stack()).Str("method", info.FullMethod).Msg("grpc handler panicked")
				err = toStatus(errInternal)
			}
		}()
		return handler(ctx, req)
	}
}

func recoverStreamInterceptor(logger zerolog.Logger) gogrpc.StreamServerInterceptor {
	return func(srv any, ss gogrpc.ServerStream, info *gogrpc.StreamServerInfo, handler gogrpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error().Interface("panic", r).Bytes("stack", debug.Stack()).Str("method", info.FullMethod).Msg("grpc stream panicked")
				err = toStatus(errInternal)
			}
		}()
		return handler(srv, ss)
	}
}

func logInterceptor(logger zerolog.Logger) gogrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *gogrpc.UnaryServerInfo, handler gogrpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err)

		event := logger.Info()
		switch code {
		case codes.OK:
		case codes.Internal, codes.Unknown, codes.DataLoss:
			event = logger.Error()
		default:
			event = logger.Warn()
		}
		meta := requestMeta(ctx)
		event.
			Str("method", info.FullMethod).
			Str("code", code.String()).
			Dur("latency", time.Since(start)).
			Str("ip", meta.IP).
			Str("user_agent", meta.UserAgent).
			Msg("grpc_request")
		return resp, err
	}
}

func rateLimitInterceptor(limited map[string]*rateLimiter) gogrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *gogrpc.UnaryServerInfo, handler gogrpc.UnaryHandler) (any, error) {
		if limiter, ok := limited[info.FullMethod]; ok && !limiter.allow(requestMeta(ctx).IP) {
			return nil, toStatus(errRateLimited)
		}
		return handler(ctx, req)
	}
}
