package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseURL  string
	Port         string
	LogLevel     string
	Environment  string
	WebhookToken string
	// ServiceToken authenticates outbound calls to the reporter, which requires
	// it on every /api/v1 route.
	ServiceToken string
	MaxBodyBytes int64
	TestMode     bool
	ReporterURL  string

	// Inbound HTTP rate limiting (design review P5). Applies per route;
	// /healthz, /ready and /metrics are exempt. Webhook routes carry their
	// own (higher) budget because alert floods are the expected load shape.
	RateLimitRPS          float64
	RateLimitBurst        int
	WebhookRateLimitRPS   float64
	WebhookRateLimitBurst int

	// Circuit breaker for the database dependency (design review P2:
	// centralized; was hard-coded 3 / 30s in handlers).
	CircuitBreakerFailureThreshold int
	CircuitBreakerResetTimeout     time.Duration

	// Minimum severity that triggers an event-driven alert forward to the
	// reporter (default "high" => high/critical). Mirrors the reporter's
	// HIGH_SEVERITY_THRESHOLD so ingest does not forward low-severity events;
	// the reporter re-checks the same threshold authoritatively.
	HighSeverityThreshold string
}

func Load() Config {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/etc/pamawas/")
	v.SetEnvPrefix("PAMAWAS_INGEST")
	// Avoid AutomaticEnv(): Kubernetes sets PAMAWAS_INGEST_PORT=tcp://IP:PORT, which would override config.
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
	if err := v.BindEnv("service_token", "PAMAWAS_SERVICE_TOKEN"); err != nil {
		panic(fmt.Sprintf("failed to bind PAMAWAS_SERVICE_TOKEN env: %v", err))
	}
	if err := v.BindEnv("reporter_url", "REPORTER_URL", "PAMAWAS_INGEST_REPORTER_URL"); err != nil {
		panic(fmt.Sprintf("failed to bind REPORTER_URL env: %v", err))
	}
	if err := v.BindEnv("rate_limit_rps", "RATE_LIMIT_RPS"); err != nil {
		panic(fmt.Sprintf("failed to bind RATE_LIMIT_RPS env: %v", err))
	}
	if err := v.BindEnv("rate_limit_burst", "RATE_LIMIT_BURST"); err != nil {
		panic(fmt.Sprintf("failed to bind RATE_LIMIT_BURST env: %v", err))
	}
	if err := v.BindEnv("webhook_rate_limit_rps", "WEBHOOK_RATE_LIMIT_RPS"); err != nil {
		panic(fmt.Sprintf("failed to bind WEBHOOK_RATE_LIMIT_RPS env: %v", err))
	}
	if err := v.BindEnv("webhook_rate_limit_burst", "WEBHOOK_RATE_LIMIT_BURST"); err != nil {
		panic(fmt.Sprintf("failed to bind WEBHOOK_RATE_LIMIT_BURST env: %v", err))
	}
	if err := v.BindEnv("circuit_breaker_failure_threshold", "CIRCUIT_BREAKER_FAILURE_THRESHOLD"); err != nil {
		panic(fmt.Sprintf("failed to bind CIRCUIT_BREAKER_FAILURE_THRESHOLD env: %v", err))
	}
	if err := v.BindEnv("circuit_breaker_reset_timeout", "CIRCUIT_BREAKER_RESET_TIMEOUT"); err != nil {
		panic(fmt.Sprintf("failed to bind CIRCUIT_BREAKER_RESET_TIMEOUT env: %v", err))
	}
	if err := v.BindEnv("high_severity_threshold", "HIGH_SEVERITY_THRESHOLD", "PAMAWAS_INGEST_HIGH_SEVERITY_THRESHOLD"); err != nil {
		panic(fmt.Sprintf("failed to bind HIGH_SEVERITY_THRESHOLD env: %v", err))
	}

	// Defaults
	v.SetDefault("port", "8080")
	v.SetDefault("reporter_url", "http://pamawas-reporter:8080")
	v.SetDefault("log_level", "info")
	v.SetDefault("environment", "development")
	v.SetDefault("max_body_bytes", 1048576) // 1 MiB
	v.SetDefault("test_mode", false)
	v.SetDefault("rate_limit_rps", 10)
	v.SetDefault("rate_limit_burst", 20)
	v.SetDefault("webhook_rate_limit_rps", 50)
	v.SetDefault("webhook_rate_limit_burst", 100)
	v.SetDefault("circuit_breaker_failure_threshold", 3)
	v.SetDefault("circuit_breaker_reset_timeout", "30s")
	v.SetDefault("high_severity_threshold", "high")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			panic(fmt.Sprintf("failed to read config: %v", err))
		}
		// Config file not found; continue with env vars and defaults
	}

	cfg := Config{
		DatabaseURL:                    v.GetString("database_url"),
		Port:                           v.GetString("port"),
		LogLevel:                       v.GetString("log_level"),
		Environment:                    v.GetString("environment"),
		WebhookToken:                   v.GetString("webhook_token"),
		ServiceToken:                   v.GetString("service_token"),
		MaxBodyBytes:                   v.GetInt64("max_body_bytes"),
		TestMode:                       v.GetBool("test_mode"),
		ReporterURL:                    v.GetString("reporter_url"),
		RateLimitRPS:                   v.GetFloat64("rate_limit_rps"),
		RateLimitBurst:                 v.GetInt("rate_limit_burst"),
		WebhookRateLimitRPS:            v.GetFloat64("webhook_rate_limit_rps"),
		WebhookRateLimitBurst:          v.GetInt("webhook_rate_limit_burst"),
		CircuitBreakerFailureThreshold: v.GetInt("circuit_breaker_failure_threshold"),
		CircuitBreakerResetTimeout:     v.GetDuration("circuit_breaker_reset_timeout"),
		HighSeverityThreshold:          v.GetString("high_severity_threshold"),
	}

	if err := cfg.Validate(); err != nil {
		panic(fmt.Sprintf("invalid configuration: %v", err))
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
	if c.RateLimitRPS <= 0 {
		return fmt.Errorf("rate_limit_rps must be positive")
	}
	if c.RateLimitBurst < 1 {
		return fmt.Errorf("rate_limit_burst must be >= 1")
	}
	if c.WebhookRateLimitRPS <= 0 {
		return fmt.Errorf("webhook_rate_limit_rps must be positive")
	}
	if c.WebhookRateLimitBurst < 1 {
		return fmt.Errorf("webhook_rate_limit_burst must be >= 1")
	}
	if !c.TestMode && c.WebhookToken == "" {
		return fmt.Errorf("webhook_token is required outside test mode (set PAMAWAS_WEBHOOK_TOKEN or enable TEST_MODE)")
	}
	if !c.TestMode && c.ServiceToken == "" {
		return fmt.Errorf("service_token is required outside test mode (set PAMAWAS_SERVICE_TOKEN): the reporter rejects unauthenticated callers")
	}
	if c.CircuitBreakerFailureThreshold < 1 {
		return fmt.Errorf("circuit_breaker_failure_threshold must be >= 1, got %d", c.CircuitBreakerFailureThreshold)
	}
	if c.CircuitBreakerResetTimeout <= 0 {
		return fmt.Errorf("circuit_breaker_reset_timeout must be > 0, got %s", c.CircuitBreakerResetTimeout)
	}
	switch strings.ToLower(c.HighSeverityThreshold) {
	// "unknown" is the absence of a gate: it ranks below every real severity,
	// so every event passes. Use it when all alerts must flow through.
	case "unknown", "debug", "info", "warning", "high", "critical":
	default:
		return fmt.Errorf("high_severity_threshold must be one of unknown|debug|info|warning|high|critical, got %q", c.HighSeverityThreshold)
	}
	return nil
}
