package service

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"api/internal/core/port"
)

// Live events: what changed, so open pages refetch it instead of polling. Declare each type here and mirror it in
// joseph-app/hooks/use-live.ts (LiveEventType)
const (
	EventSystemPing    = "system.ping" // {}: test event, see `make live/ping`
	livePublishTimeout = 2 * time.Second
)

// LiveService publishes live events and reads them back for the WebSocket. A nil *LiveService is valid and
// publishes nothing, so services keep working without Redis
type LiveService struct {
	bus    port.LiveBus
	logger zerolog.Logger
}

func NewLiveService(bus port.LiveBus, logger zerolog.Logger) *LiveService {
	return &LiveService{bus: bus, logger: logger}
}

// Notify publishes after the change is stored; a failure only means open pages update on their next load
func (s *LiveService) Notify(ctx context.Context, kind string, data map[string]string) {
	if s == nil || s.bus == nil {
		return
	}
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), livePublishTimeout)
	defer cancel()
	if err := s.bus.Publish(publishCtx, port.LiveEvent{Type: kind, Data: data}); err != nil {
		s.logger.Warn().Err(err).Str("event", kind).Msg("live publish failed")
	}
}

// Read serves the browser WebSocket
func (s *LiveService) Read(ctx context.Context, cursor string, block time.Duration) ([]port.StreamedEvent, string, error) {
	return s.bus.Read(ctx, cursor, block)
}
