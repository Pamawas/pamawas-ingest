package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/Pamawas/pamawas-ingest/config"
	"github.com/Pamawas/pamawas-ingest/handlers"
	"github.com/Pamawas/pamawas-ingest/metrics"
	"github.com/Pamawas/pamawas-ingest/middleware"
	"github.com/Pamawas/pamawas-ingest/otel"
)

func main() {
	cfg := config.Load()
	initLogger(cfg)
	printStartupBanner(loadVersion(), cfg.Port)

	// Initialize OpenTelemetry tracing
	otelShutdown, err := otel.InitTracer(otel.Config{
		ServiceName:  "pamawas-ingest",
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		Insecure:     true,
		SampleRatio:  1.0,
		Enabled:      os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "",
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize OpenTelemetry")
	}
	defer func() {
		if shutdownErr := otelShutdown(context.Background()); shutdownErr != nil {
			log.Error().Err(shutdownErr).Msg("Error shutting down OpenTelemetry")
		}
	}()

	log.Info().
		Str("port", cfg.Port).
		Str("environment", cfg.Environment).
		Str("log_level", cfg.LogLevel).
		Msg("Starting pamawas-ingest")

	// Connect to database with retries
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Error opening database")
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close database connection")
		}
	}()

	// Test connection with retries
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := 0; i < 30; i++ {
		if err := db.PingContext(ctx); err == nil {
			break
		}
		log.Printf("Waiting for database... (%d/30)", i+1)
		time.Sleep(1 * time.Second)
	}

	if err := db.PingContext(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database after retries")
	}

	// Set connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	log.Info().Msg("Connected to database")

	// Initialize metrics
	m := metrics.NewMetrics()

	// Initialize handlers
	h := handlers.NewHandler(db, cfg, m)

	r := newRouter(h, cfg)

	// Create server
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Info().Msg("Shutdown signal received, stopping server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("Server forced to shutdown")
		}
	}()

	log.Info().Str("port", cfg.Port).Msg("Starting server")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("Server failed")
	}

	log.Info().Msg("Server stopped gracefully")
}

func newRouter(h *handlers.Handler, cfg config.Config) *mux.Router {
	r := mux.NewRouter()
	r.Use(middleware.LoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.ErrorLoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.BodyLimitMiddleware(cfg.MaxBodyBytes))

	apiV1 := r.PathPrefix("/api/v1").Subrouter()
	apiV1.HandleFunc("/webhooks/grafana", h.GrafanaWebhook).Methods("POST")
	apiV1.HandleFunc("/webhooks/generic", h.GenericWebhook).Methods("POST")

	r.HandleFunc("/healthz", h.HealthHandler).Methods("GET")
	r.HandleFunc("/ready", h.ReadyHandler).Methods("GET")
	r.Handle("/metrics", h.MetricsHandler())

	return r
}

func initLogger(cfg config.Config) {
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	if cfg.Environment == "development" {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger().Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	} else {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}
}
