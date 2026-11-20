package redisclient

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"api/internal/core/port"
)

// Stream of live events shared by every API instance; pages only need what happens while they are open
const (
	liveStream    = "lt-edu:live"
	liveMaxLength = 1000
)

type LiveBus struct {
	rdb *redis.Client
}

func NewLiveBus(client *Client) *LiveBus {
	return &LiveBus{rdb: client.rdb}
}

func (b *LiveBus) Publish(ctx context.Context, event port.LiveEvent) error {
	data, err := json.Marshal(event.Data)
	if err != nil {
		return err
	}
	return b.rdb.XAdd(ctx, &redis.XAddArgs{Stream: liveStream, MaxLen: liveMaxLength, Approx: true, Values: map[string]any{"type": event.Type, "data": data}}).Err()
}

func (b *LiveBus) Read(ctx context.Context, cursor string, block time.Duration) ([]port.StreamedEvent, string, error) {
	return readStream(ctx, b.rdb, liveStream, cursor, block)
}

func readStream(ctx context.Context, rdb *redis.Client, stream, cursor string, block time.Duration) ([]port.StreamedEvent, string, error) {
	if cursor == "" {
		cursor = "$"
	}
	streams, err := rdb.XRead(ctx, &redis.XReadArgs{Streams: []string{stream, cursor}, Block: block, Count: 200}).Result()
	if errors.Is(err, redis.Nil) {
		// Nothing new within the block time
		return nil, cursor, nil
	}
	if err != nil {
		return nil, cursor, err
	}
	var out []port.StreamedEvent
	for _, s := range streams {
		for _, message := range s.Messages {
			kind, _ := message.Values["type"].(string)
			data, _ := message.Values["data"].(string)
			out = append(out, port.StreamedEvent{ID: message.ID, Type: kind, Data: []byte(data)})
			cursor = message.ID
		}
	}
	return out, cursor, nil
}
