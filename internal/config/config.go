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
	// Browser origins allowed to open the live WebSocket (lt-edu-app's public origin)
	WSOrigins []string

	// Public URL of lt-edu-app (links in mail)
	SiteURL    string
	SessionTTL time.Duration
	// Base64 of 32 bytes: encrypts TOTP secrets at rest
	SecretsKey string
	// Superadmin created on start when missing
	AdminEmail    string
	AdminPassword string

	// Google OAuth client (sign in with Google); the redirect is lt-edu-app's /api/auth/google/callback
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	TurnstileSecretKey string

	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageUseSSL    bool
	StorageRegion    string
	StorageBucket    string

	// LiveKit (video calls inside the platform). Empty URL: only external links (Google Meet) are offered
	LiveKitURL       string
	LiveKitAPIKey    string
	LiveKitAPISecret string

	// How often the platform URL of each institution is probed for the availability log (0 turns it off)
	UptimeInterval time.Duration
}

func Load() Config {
	v := viper.New()
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	v.SetDefault("APP__ENV", "development")
	v.SetDefault("APP__NAME", "lt-edu-api")
	v.SetDefault("APP__HOST__PORT", "8080")
	v.SetDefault("APP__LOG__LEVEL", "debug")
	v.SetDefault("APP__GRPC__PORT", "9090")
	v.SetDefault("APP__HTTP__TRUSTED_PROXIES", "")
	v.SetDefault("APP__WS__ORIGINS", "")
	v.SetDefault("APP__UPTIME__INTERVAL", "0")

	v.SetDefault("APP__DB__HOST", "localhost")
	v.SetDefault("APP__DB__PORT", "5432")
	v.SetDefault("APP__DB__USER", "ltedu")
	v.SetDefault("APP__DB__PASSWORD", "ltedu_dev")
	v.SetDefault("APP__DB__NAME", "ltedu_db")
	v.SetDefault("APP__DB__SSLMODE", "disable")

	v.SetDefault("APP__REDIS__HOST", "localhost")
	v.SetDefault("APP__REDIS__PORT", "6379")
	v.SetDefault("APP__REDIS__PASSWORD", "")
	v.SetDefault("APP__REDIS__DB", "0")

	v.SetDefault("APP__HEALTH__PING_TIMEOUT", "2s")

	v.SetDefault("APP__SITE__URL", "http://localhost:5173")
	v.SetDefault("APP__SESSION__TTL", "168h")
	v.SetDefault("APP__SECRETS__KEY", "")
	v.SetDefault("APP__ADMIN__EMAIL", "")
	v.SetDefault("APP__ADMIN__PASSWORD", "")
	v.SetDefault("APP__GOOGLE__OAUTH__CLIENT_ID", "")
	v.SetDefault("APP__GOOGLE__OAUTH__CLIENT_SECRET", "")
	v.SetDefault("APP__GOOGLE__OAUTH__REDIRECT_URL", "http://localhost:5173/api/auth/google/callback")
	v.SetDefault("APP__GOOGLE__SMTP__HOST", "")
	v.SetDefault("APP__GOOGLE__SMTP__PORT", "587")
	v.SetDefault("APP__GOOGLE__SMTP__USER", "")
	v.SetDefault("APP__GOOGLE__SMTP__PASSWORD", "")
	v.SetDefault("APP__GOOGLE__SMTP__FROM", "")

	v.SetDefault("APP__TURNSTILE__SECRET_KEY", "")

	v.SetDefault("APP__STORAGE__ENDPOINT", "localhost:9000")
	v.SetDefault("APP__STORAGE__ACCESS_KEY", "")
	v.SetDefault("APP__STORAGE__SECRET_KEY", "")
	v.SetDefault("APP__STORAGE__USE_SSL", false)
	v.SetDefault("APP__STORAGE__REGION", "us-east-1")
	v.SetDefault("APP__STORAGE__BUCKET", "lt-edu")

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

		SiteURL:        v.GetString("APP__SITE__URL"),
		SessionTTL:     v.GetDuration("APP__SESSION__TTL"),
		UptimeInterval: v.GetDuration("APP__UPTIME__INTERVAL"),
		SecretsKey:     v.GetString("APP__SECRETS__KEY"),
		AdminEmail:     v.GetString("APP__ADMIN__EMAIL"),
		AdminPassword:  v.GetString("APP__ADMIN__PASSWORD"),

		GoogleClientID:     v.GetString("APP__GOOGLE__OAUTH__CLIENT_ID"),
		GoogleClientSecret: v.GetString("APP__GOOGLE__OAUTH__CLIENT_SECRET"),
		GoogleRedirectURL:  v.GetString("APP__GOOGLE__OAUTH__REDIRECT_URL"),

		SMTPHost:     v.GetString("APP__GOOGLE__SMTP__HOST"),
		SMTPPort:     v.GetInt("APP__GOOGLE__SMTP__PORT"),
		SMTPUser:     v.GetString("APP__GOOGLE__SMTP__USER"),
		SMTPPassword: v.GetString("APP__GOOGLE__SMTP__PASSWORD"),
		SMTPFrom:     v.GetString("APP__GOOGLE__SMTP__FROM"),

		TurnstileSecretKey: v.GetString("APP__TURNSTILE__SECRET_KEY"),

		StorageEndpoint:  v.GetString("APP__STORAGE__ENDPOINT"),
		StorageAccessKey: v.GetString("APP__STORAGE__ACCESS_KEY"),
		StorageSecretKey: v.GetString("APP__STORAGE__SECRET_KEY"),
		StorageUseSSL:    v.GetBool("APP__STORAGE__USE_SSL"),
		StorageRegion:    v.GetString("APP__STORAGE__REGION"),
		StorageBucket:    v.GetString("APP__STORAGE__BUCKET"),

		LiveKitURL:       v.GetString("APP__LIVEKIT__URL"),
		LiveKitAPIKey:    v.GetString("APP__LIVEKIT__API_KEY"),
		LiveKitAPISecret: v.GetString("APP__LIVEKIT__API_SECRET"),
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
