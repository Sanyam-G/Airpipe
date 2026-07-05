package relay

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port            string
	AllowedOrigins  []string
	AllowAnyOrigin  bool
	RateLimitPerMin int
	LogFormat       string
	MaxUploadBytes  int64
	FileExpiry      time.Duration
}

func LoadConfig() Config {
	c := Config{
		Port:            getenv("PORT", "8080"),
		RateLimitPerMin: getenvInt("AIRPIPE_RATE_LIMIT_PER_MIN", 60),
		LogFormat:       getenv("AIRPIPE_LOG_FORMAT", "json"),
		MaxUploadBytes:  int64(getenvInt("AIRPIPE_MAX_UPLOAD_MB", 500)) << 20,
		FileExpiry:      getenvDuration("AIRPIPE_FILE_EXPIRY", 10*time.Minute),
	}
	// Same-origin browsers are always allowed by the checker; this list is
	// only for pages served from a different domain than the relay.
	raw := strings.TrimSpace(os.Getenv("AIRPIPE_ALLOWED_ORIGINS"))
	if raw == "" {
		c.AllowedOrigins = nil
	} else if raw == "*" {
		c.AllowAnyOrigin = true
	} else {
		for _, o := range strings.Split(raw, ",") {
			if v := strings.TrimSpace(o); v != "" {
				c.AllowedOrigins = append(c.AllowedOrigins, v)
			}
		}
	}
	return c
}

func NewLogger(format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if format == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}
