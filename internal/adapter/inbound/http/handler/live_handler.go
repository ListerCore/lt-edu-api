package handler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"api/internal/core/port"
	"api/internal/core/service"
)

const (
	liveBlock        = 15 * time.Second
	livePingInterval = 25 * time.Second
	liveWriteTimeout = 10 * time.Second
)

type liveMessage struct {
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// streamReader reads a Redis stream after cursor, blocking up to block
type streamReader func(ctx context.Context, cursor string, block time.Duration) ([]port.StreamedEvent, string, error)

// LiveHandler serves the browser WebSocket: which records changed, so open pages refetch them
type LiveHandler struct {
	live   *service.LiveService
	logger zerolog.Logger
}

func NewLiveHandler(live *service.LiveService, logger zerolog.Logger) *LiveHandler {
	return &LiveHandler{live: live, logger: logger}
}

// Stream relays the live stream. Origins guards against cross-site WebSocket hijacking: with no origin configured
// every upgrade is refused
func (h *LiveHandler) Stream(origins []string) fiber.Handler {
	if len(origins) == 0 {
		h.logger.Warn().Msg("APP__WS__ORIGINS is empty: the live WebSocket refuses every connection")
		return func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusForbidden) }
	}
	return streamLive(h.live.Read, origins, h.logger, "live")
}

// streamLive relays a Redis stream to a WebSocket until either side goes away (so it works with several API
// instances). The first message is {"type":"ready"}; {"type":"degraded"} means Redis is failing and it retries
func streamLive(read streamReader, origins []string, logger zerolog.Logger, name string) fiber.Handler {
	return websocket.New(func(conn *websocket.Conn) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// The browser sends nothing; reading only detects the close (and answers pings)
		go func() {
			defer cancel()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()

		write := func(message liveMessage) error {
			_ = conn.SetWriteDeadline(time.Now().Add(liveWriteTimeout))
			return conn.WriteJSON(message)
		}
		if write(liveMessage{Type: "ready"}) != nil {
			return
		}

		cursor, lastPing := "$", time.Now()
		for ctx.Err() == nil {
			events, next, err := read(ctx, cursor, liveBlock)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logger.Warn().Err(err).Str("stream", name).Msg("live read failed")
				if write(liveMessage{Type: "degraded"}) != nil {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
				continue
			}
			cursor = next
			for _, event := range events {
				if write(liveMessage{Type: event.Type, ID: event.ID, Data: event.Data}) != nil {
					return
				}
			}
			if time.Since(lastPing) > livePingInterval {
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(liveWriteTimeout)) != nil {
					return
				}
				lastPing = time.Now()
			}
		}
	}, websocket.Config{Origins: origins})
}
