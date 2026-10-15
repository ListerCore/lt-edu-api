package service

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/port"
)

type HealthService struct {
	db          port.Pinger
	redis       port.Pinger
	serviceName string
	pingTimeout time.Duration
}

func NewHealthService(db, redis port.Pinger, serviceName string, pingTimeout time.Duration) *HealthService {
	return &HealthService{db: db, redis: redis, serviceName: serviceName, pingTimeout: pingTimeout}
}

type CheckOptions struct {
	IncludeDB    bool
	IncludeRedis bool
}

// Check reports the overall health of the service plus, when asked, the
// status of the backing components. Component-level errors are logged
// through the supplied logger and then dropped: the response that reaches
// an anonymous caller never carries a raw error string from the database
// or Redis. Operators diagnose problems from the server logs, not by
// scraping the public endpoint.
func (s *HealthService) Check(ctx context.Context, clientIP string, opts CheckOptions, logger zerolog.Logger) domain.Health {
	health := domain.Health{
		Status:    domain.StatusOK,
		Service:   s.serviceName,
		Timestamp: time.Now().UTC(),
	}

	if opts.IncludeDB {
		status := s.pingComponent(ctx, s.db)
		health.DB = s.recordAndStrip("db", status, clientIP, logger)
		if status.Status != domain.StatusOK {
			health.Status = domain.StatusDegraded
		}
	}

	if opts.IncludeRedis {
		status := s.pingComponent(ctx, s.redis)
		health.Redis = s.recordAndStrip("redis", status, clientIP, logger)
		if status.Status != domain.StatusOK {
			health.Status = domain.StatusDegraded
		}
	}

	return health
}

func (s *HealthService) pingComponent(ctx context.Context, pinger port.Pinger) *domain.ComponentStatus {
	if pinger == nil {
		return &domain.ComponentStatus{Status: domain.StatusDegraded, Error: "not configured"}
	}

	ctx, cancel := context.WithTimeout(ctx, s.pingTimeout)
	defer cancel()

	start := time.Now()
	err := pinger.Ping(ctx)
	latency := time.Since(start)

	if err != nil {
		return &domain.ComponentStatus{Status: domain.StatusDegraded, Latency: latency.String(), Error: err.Error()}
	}

	return &domain.ComponentStatus{Status: domain.StatusOK, Latency: latency.String()}
}

// recordAndStrip logs the component error (if any) and returns a copy
// with the Error field cleared, so the caller never sees it.
func (s *HealthService) recordAndStrip(name string, status *domain.ComponentStatus, clientIP string, logger zerolog.Logger) *domain.ComponentStatus {
	if status == nil {
		return nil
	}
	if status.Error != "" {
		logger.Warn().
			Str("component", name).
			Str("client_ip", clientIP).
			Str("latency", status.Latency).
			Msg("health component degraded: " + status.Error)
		status.Error = ""
	}
	return status
}
