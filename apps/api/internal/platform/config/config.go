// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
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
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	switch c.Env {
	case "local", "ci", "staging", "production":
		return nil
	default:
		return fmt.Errorf("APP_ENV %q is not one of local, ci, staging, production", c.Env)
	}
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
