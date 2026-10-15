package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	AppEnv        string
	AppName       string
	Port          string
	LogLevel      string
	DatabaseURL   string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	PingTimeout   time.Duration
}

func Load() Config {
	v := viper.New()
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	v.SetDefault("APP__ENV", "development")
	v.SetDefault("APP__NAME", "joseph-api")
	v.SetDefault("APP__HOST__PORT", "8080")
	v.SetDefault("APP__LOG__LEVEL", "debug")

	v.SetDefault("APP__DB__HOST", "localhost")
	v.SetDefault("APP__DB__PORT", "5432")
	v.SetDefault("APP__DB__USER", "joseph")
	v.SetDefault("APP__DB__PASSWORD", "joseph_dev")
	v.SetDefault("APP__DB__NAME", "joseph_db")
	v.SetDefault("APP__DB__SSLMODE", "disable")

	v.SetDefault("APP__REDIS__HOST", "localhost")
	v.SetDefault("APP__REDIS__PORT", "6379")
	v.SetDefault("APP__REDIS__PASSWORD", "")
	v.SetDefault("APP__REDIS__DB", "0")

	v.SetDefault("APP__HEALTH__PING_TIMEOUT", "2s")

	_ = v.ReadInConfig()

	databaseURL := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		v.GetString("APP__DB__USER"),
		v.GetString("APP__DB__PASSWORD"),
		v.GetString("APP__DB__HOST"),
		v.GetString("APP__DB__PORT"),
		v.GetString("APP__DB__NAME"),
		v.GetString("APP__DB__SSLMODE"),
	)

	redisAddr := fmt.Sprintf("%s:%s", v.GetString("APP__REDIS__HOST"), v.GetString("APP__REDIS__PORT"))

	return Config{
		AppEnv:        v.GetString("APP__ENV"),
		AppName:       v.GetString("APP__NAME"),
		Port:          v.GetString("APP__HOST__PORT"),
		LogLevel:      v.GetString("APP__LOG__LEVEL"),
		DatabaseURL:   databaseURL,
		RedisAddr:     redisAddr,
		RedisPassword: v.GetString("APP__REDIS__PASSWORD"),
		RedisDB:       v.GetInt("APP__REDIS__DB"),
		PingTimeout:   v.GetDuration("APP__HEALTH__PING_TIMEOUT"),
	}
}
