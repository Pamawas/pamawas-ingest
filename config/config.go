package config

import (
	"os"
)

type Config struct {
	DatabaseURL string
	Port        string
	LogLevel    string
	Environment string
}

func Load() Config {
	cfg := Config{
		DatabaseURL: getEnv("DATABASE_URL", ""),
		Port:        getEnv("PORT", "8080"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		Environment: getEnv("ENVIRONMENT", "development"),
	}

	if cfg.DatabaseURL == "" {
		panic("DATABASE_URL environment variable not set")
	}
	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}