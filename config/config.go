package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseURL string
	Port        string
	LogLevel    string
	Environment string
}

func Load() Config {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/etc/pamawas/")
	v.SetEnvPrefix("PAMAWAS_INGEST")
	v.AutomaticEnv()

	// Defaults
	v.SetDefault("port", "8080")
	v.SetDefault("log_level", "info")
	v.SetDefault("environment", "development")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			panic(fmt.Sprintf("failed to read config: %v", err))
		}
		// Config file not found; continue with env vars and defaults
	}

	cfg := Config{
		DatabaseURL: v.GetString("database_url"),
		Port:        v.GetString("port"),
		LogLevel:    v.GetString("log_level"),
		Environment: v.GetString("environment"),
	}

	if cfg.DatabaseURL == "" {
		panic("DATABASE_URL not set (config file or PAMAWAS_INGEST_DATABASE_URL env var)")
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
	if _, err := time.ParseDuration("1h"); err != nil {
		// Just a sanity check
	}
	return nil
}