package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseURL  string
	Port         string
	LogLevel     string
	Environment  string
	WebhookToken string
	MaxBodyBytes int64
	TestMode     bool
}

func Load() Config {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/etc/pamawas/")
	v.SetEnvPrefix("PAMAWAS_INGEST")
	// Don't use AutomaticEnv() - Kubernetes injects PAMAWAS_INGEST_PORT=tcp://IP:PORT
	// which would override our config. Explicitly bind only what we need.
	if err := v.BindEnv("database_url", "DATABASE_URL", "PAMAWAS_INGEST_DATABASE_URL"); err != nil {
		panic(fmt.Sprintf("failed to bind DATABASE_URL env: %v", err))
	}
	if err := v.BindEnv("port", "PORT", "PAMAWAS_INGEST_PORT"); err != nil {
		panic(fmt.Sprintf("failed to bind PORT env: %v", err))
	}
	if err := v.BindEnv("log_level", "LOG_LEVEL", "PAMAWAS_INGEST_LOG_LEVEL"); err != nil {
		panic(fmt.Sprintf("failed to bind LOG_LEVEL env: %v", err))
	}
	if err := v.BindEnv("environment", "ENVIRONMENT", "PAMAWAS_INGEST_ENVIRONMENT"); err != nil {
		panic(fmt.Sprintf("failed to bind ENVIRONMENT env: %v", err))
	}
	if err := v.BindEnv("webhook_token", "PAMAWAS_WEBHOOK_TOKEN"); err != nil {
		panic(fmt.Sprintf("failed to bind PAMAWAS_WEBHOOK_TOKEN env: %v", err))
	}
	if err := v.BindEnv("max_body_bytes", "MAX_BODY_BYTES", "PAMAWAS_INGEST_MAX_BODY_BYTES"); err != nil {
		panic(fmt.Sprintf("failed to bind MAX_BODY_BYTES env: %v", err))
	}
	if err := v.BindEnv("test_mode", "TEST_MODE", "PAMAWAS_INGEST_TEST_MODE"); err != nil {
		panic(fmt.Sprintf("failed to bind TEST_MODE env: %v", err))
	}

	// Defaults
	v.SetDefault("port", "8080")
	v.SetDefault("log_level", "info")
	v.SetDefault("environment", "development")
	v.SetDefault("max_body_bytes", 1048576) // 1 MiB
	v.SetDefault("test_mode", false)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			panic(fmt.Sprintf("failed to read config: %v", err))
		}
		// Config file not found; continue with env vars and defaults
	}

	cfg := Config{
		DatabaseURL:  v.GetString("database_url"),
		Port:         v.GetString("port"),
		LogLevel:     v.GetString("log_level"),
		Environment:  v.GetString("environment"),
		WebhookToken: v.GetString("webhook_token"),
		MaxBodyBytes: v.GetInt64("max_body_bytes"),
		TestMode:     v.GetBool("test_mode"),
	}

	if cfg.DatabaseURL == "" {
		panic("DATABASE_URL not set (config file or PAMAWAS_INGEST_DATABASE_URL / DATABASE_URL env var)")
	}
	return cfg
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("database_url is required")
	}
	if c.Port == "" {
		return fmt.Errorf("port is required")
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("max_body_bytes must be positive")
	}
	return nil
}
