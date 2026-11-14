package config

import (
	"fmt"
	"log/slog"
	"os"
)

// DBConfig is the minimal configuration for tools that only talk to the database, such as the
// bootstrap CLI. It reads DATABASE_URL and APP_ENV and nothing else.
type DBConfig struct {
	Env         string
	DatabaseURL string
}

// LogValue keeps the database password out of logs.
func (c DBConfig) LogValue() slog.Value {
	return slog.GroupValue(slog.String("env", c.Env), slog.String("database_url", "[redacted]"))
}

// LoadDB reads and validates the database settings.
func LoadDB() (DBConfig, error) {
	cfg := DBConfig{Env: getenv("APP_ENV", "local"), DatabaseURL: os.Getenv("DATABASE_URL")}
	if err := validateDB(cfg.DatabaseURL, cfg.Env); err != nil {
		return DBConfig{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
