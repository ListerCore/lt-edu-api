package port

import (
	"context"
	"time"
)

// LiveEvent tells open pages that something changed. Carry ids only, never personal data: each page refetches what
// it shows through the API, so every viewer still sees only what it is allowed to
type LiveEvent struct {
	Type string
	Data map[string]string
}

// StreamedEvent is one event read back from the stream, with its stream id
type StreamedEvent struct {
	ID   string
	Type string
	Data []byte
}

// LiveBus is the channel between the services and the browser WebSocket (Redis Streams, shared by every instance)
type LiveBus interface {
	Publish(ctx context.Context, event LiveEvent) error
	// Read blocks up to block for events after cursor ("$" = only new ones) and returns them with the next cursor
	Read(ctx context.Context, cursor string, block time.Duration) ([]StreamedEvent, string, error)
}
