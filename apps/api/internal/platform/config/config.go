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
	Tick            Tick
}

// Tick configures authentication of the scheduler tick endpoint (feature 002, FR-003).
type Tick struct {
	// Auth is "oidc" (Google-signed ID token; default in staging and production) or "dev"
	// (shared test token; default in local and ci, refused elsewhere).
	Auth string
	// Audience and ServiceAccount are the expected OIDC audience and caller identity. With
	// Auth "oidc" and either one empty the tick is disabled (every call is rejected).
	Audience       string
	ServiceAccount string
	// DevToken is the bearer token accepted when Auth is "dev"; empty rejects every tick.
	DevToken string
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	// Cloud Run sets K_SERVICE. There APP_ENV must be explicit: the "local" fallback would
	// default the tick to unauthenticated dev mode.
	if os.Getenv("K_SERVICE") != "" && os.Getenv("APP_ENV") == "" {
		return Config{}, errors.New("config: APP_ENV is required when running on Cloud Run (K_SERVICE is set)")
	}
	cfg := Config{
		Env:             getenv("APP_ENV", "local"),
		HTTPAddr:        getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		ShutdownTimeout: 10 * time.Second,
	}
	cfg.Tick = Tick{
		Auth:           getenv("TICK_AUTH", defaultTickAuth(cfg.Env)),
		Audience:       os.Getenv("TICK_AUDIENCE"),
		ServiceAccount: os.Getenv("TICK_SERVICE_ACCOUNT"),
		DevToken:       os.Getenv("TICK_DEV_TOKEN"),
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
	default:
		return fmt.Errorf("APP_ENV %q is not one of local, ci, staging, production", c.Env)
	}
	return c.validateTick()
}

// validateTick refuses the test-only dev authenticator outside local and ci. The error names the
// setting and never includes TICK_DEV_TOKEN.
func (c Config) validateTick() error {
	switch c.Tick.Auth {
	case "oidc":
		return nil
	case "dev":
		if os.Getenv("K_SERVICE") != "" {
			return errors.New("TICK_AUTH=dev is not allowed on Cloud Run (K_SERVICE is set)")
		}
		if c.Env == "staging" || c.Env == "production" {
			return fmt.Errorf("TICK_AUTH=dev is not allowed when APP_ENV is %s", c.Env)
		}
		return nil
	default:
		return fmt.Errorf("TICK_AUTH %q is not one of oidc, dev", c.Tick.Auth)
	}
}

func defaultTickAuth(env string) string {
	if env == "local" || env == "ci" {
		return "dev"
	}
	return "oidc"
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
