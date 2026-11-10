package grpc

import (
	"context"

	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	"api/internal/adapter/inbound/grpc/gen/systemv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// systemServer exposes service.HealthService; same answer as GET /api/v1/health
type systemServer struct {
	systemv1.UnimplementedSystemServiceServer
	health *service.HealthService
	logger zerolog.Logger
}

func (s *systemServer) GetHealth(ctx context.Context, req *systemv1.GetHealthRequest) (*systemv1.GetHealthResponse, error) {
	h := s.health.Check(ctx, requestMeta(ctx).IP, service.CheckOptions{IncludeDB: req.GetIncludeDb(), IncludeRedis: req.GetIncludeRedis()}, s.logger)
	return &systemv1.GetHealthResponse{
		Status:    string(h.Status),
		Service:   h.Service,
		Timestamp: timestamppb.New(h.Timestamp),
		Db:        toComponent(h.DB),
		Redis:     toComponent(h.Redis),
	}, nil
}

func toComponent(c *domain.ComponentStatus) *systemv1.ComponentStatus {
	if c == nil {
		return nil
	}
	return &systemv1.ComponentStatus{Status: string(c.Status), Latency: c.Latency}
}
