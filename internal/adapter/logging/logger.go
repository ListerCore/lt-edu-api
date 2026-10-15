package logging

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"

	"api/internal/config"
)

func New(cfg config.Config) zerolog.Logger {
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}

	zerolog.TimeFieldFormat = time.RFC3339

	var output io.Writer = os.Stdout
	if cfg.AppEnv != "production" {
		output = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}

	return zerolog.New(output).
		Level(level).
		With().
		Timestamp().
		Str("service", cfg.AppName).
		Logger()
}
