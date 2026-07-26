package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr        string
	PublicBaseURL     string
	DatabaseDSN       string
	JWTSecret         string
	JWTTTL            time.Duration
	AllowRegistration bool
}

func Load() (Config, error) {
	ttl, err := time.ParseDuration(valueOrDefault("JWT_TTL", "24h"))
	if err != nil {
		return Config{}, fmt.Errorf("JWT_TTL: %w", err)
	}
	allowRegistration, err := strconv.ParseBool(valueOrDefault("ALLOW_REGISTRATION", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("ALLOW_REGISTRATION: %w", err)
	}

	cfg := Config{
		ListenAddr:        valueOrDefault("LISTEN_ADDR", "127.0.0.1:28672"),
		PublicBaseURL:     strings.TrimRight(valueOrDefault("PUBLIC_BASE_URL", "http://106.52.208.129:28671"), "/"),
		DatabaseDSN:       os.Getenv("DATABASE_DSN"),
		JWTSecret:         os.Getenv("JWT_SECRET"),
		JWTTTL:            ttl,
		AllowRegistration: allowRegistration,
	}

	if cfg.DatabaseDSN == "" {
		return Config{}, errors.New("DATABASE_DSN is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, errors.New("JWT_SECRET must contain at least 32 characters")
	}
	if !strings.HasPrefix(cfg.PublicBaseURL, "http://") && !strings.HasPrefix(cfg.PublicBaseURL, "https://") {
		return Config{}, errors.New("PUBLIC_BASE_URL must start with http:// or https://")
	}
	return cfg, nil
}

func valueOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
