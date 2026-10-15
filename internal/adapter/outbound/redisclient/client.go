package redisclient

import (
	"context"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

type Client struct {
	rdb *redis.Client
}

func New(addr, password string, db int, logger zerolog.Logger) *Client {
	redis.SetLogger(redisLogger{log: logger})

	return &Client{
		rdb: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
			DB:       db,
		}),
	}
}

func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

type redisLogger struct {
	log zerolog.Logger
}

func (l redisLogger) Printf(_ context.Context, format string, v ...interface{}) {
	l.log.Debug().Str("component", "redis").Msgf(format, v...)
}
