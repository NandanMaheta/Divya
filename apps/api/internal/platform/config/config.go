// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Config holds every setting the API needs. Values come only from the environment
// so the same binary runs locally, in CI and on Cloud Run.
type Config struct {
	Env             string
	HTTPAddr        string
	DatabaseURL     string
	ShutdownTimeout time.Duration
	Auth            AuthConfig
}

// LogValue implements slog.LogValuer so logging a Config never reveals the database password or
// any key material.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", c.Env),
		slog.String("http_addr", c.HTTPAddr),
		slog.String("database_url", "[redacted]"),
		slog.Duration("shutdown_timeout", c.ShutdownTimeout),
		slog.Any("auth", c.Auth),
	)
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		Env:             getenv("APP_ENV", "local"),
		HTTPAddr:        getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		ShutdownTimeout: 10 * time.Second,
	}
	// Cloud Run injects PORT; honour it when HTTP_ADDR is not set explicitly.
	if port := os.Getenv("PORT"); port != "" && os.Getenv("HTTP_ADDR") == "" {
		cfg.HTTPAddr = ":" + port
	}
	if err := validateDB(cfg.DatabaseURL, cfg.Env); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	auth, err := loadAuth(cfg.Env)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	cfg.Auth = auth
	return cfg, nil
}

func validateDB(databaseURL, env string) error {
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	switch env {
	case "local", "ci", "staging", "production":
		return nil
	default:
		return fmt.Errorf("APP_ENV %q is not one of local, ci, staging, production", env)
	}
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
