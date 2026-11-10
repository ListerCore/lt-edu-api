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
	GRPCPort      string
	LogLevel      string
	DatabaseURL   string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	PingTimeout   time.Duration

	// Proxies allowed to set X-Forwarded-For besides loopback and private ranges (IPs or CIDRs)
	TrustedProxies []string
	// Browser origins allowed to open the live WebSocket (joseph-app's public origin)
	WSOrigins []string

	TurnstileSecretKey string

	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageUseSSL    bool
	StorageRegion    string
	StorageBucket    string
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
	v.SetDefault("APP__GRPC__PORT", "9090")
	v.SetDefault("APP__HTTP__TRUSTED_PROXIES", "")
	v.SetDefault("APP__WS__ORIGINS", "")

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

	v.SetDefault("APP__TURNSTILE__SECRET_KEY", "")

	v.SetDefault("APP__STORAGE__ENDPOINT", "localhost:9000")
	v.SetDefault("APP__STORAGE__ACCESS_KEY", "")
	v.SetDefault("APP__STORAGE__SECRET_KEY", "")
	v.SetDefault("APP__STORAGE__USE_SSL", false)
	v.SetDefault("APP__STORAGE__REGION", "us-east-1")
	v.SetDefault("APP__STORAGE__BUCKET", "joseph")

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
		GRPCPort:      v.GetString("APP__GRPC__PORT"),
		LogLevel:      v.GetString("APP__LOG__LEVEL"),
		DatabaseURL:   databaseURL,
		RedisAddr:     redisAddr,
		RedisPassword: v.GetString("APP__REDIS__PASSWORD"),
		RedisDB:       v.GetInt("APP__REDIS__DB"),
		PingTimeout:   v.GetDuration("APP__HEALTH__PING_TIMEOUT"),

		TrustedProxies: splitList(v.GetString("APP__HTTP__TRUSTED_PROXIES")),
		WSOrigins:      splitList(v.GetString("APP__WS__ORIGINS")),

		TurnstileSecretKey: v.GetString("APP__TURNSTILE__SECRET_KEY"),

		StorageEndpoint:  v.GetString("APP__STORAGE__ENDPOINT"),
		StorageAccessKey: v.GetString("APP__STORAGE__ACCESS_KEY"),
		StorageSecretKey: v.GetString("APP__STORAGE__SECRET_KEY"),
		StorageUseSSL:    v.GetBool("APP__STORAGE__USE_SSL"),
		StorageRegion:    v.GetString("APP__STORAGE__REGION"),
		StorageBucket:    v.GetString("APP__STORAGE__BUCKET"),
	}
}

// splitList reads a comma-separated value, trimming spaces and trailing slashes of origins
func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSuffix(strings.TrimSpace(item), "/"); item != "" {
			out = append(out, item)
		}
	}
	return out
}
