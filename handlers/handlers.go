package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/Pamawas/pamawas-ingest/config"
	"github.com/Pamawas/pamawas-ingest/metrics"
	"github.com/Pamawas/pamawas-ingest/models"
)

// Handler holds dependencies for HTTP handlers
type Handler struct {
	db      *sql.DB
	cfg     config.Config
	metrics *metrics.Metrics
}

// NewHandler creates a new handler with dependencies
func NewHandler(db *sql.DB, cfg config.Config, m *metrics.Metrics) *Handler {
	return &Handler{
		db:      db,
		cfg:     cfg,
		metrics: m,
	}
}

// GrafanaWebhook handles Grafana alert webhook payload
func (h *Handler) GrafanaWebhook(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	endpoint := "grafana"

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		log.Error().Err(err).Msg("Invalid JSON in grafana webhook")
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Extract relevant fields from Grafana webhook with validation
	eventID := uuid.NewString()
	source := "grafana"
	eventType := "alert"

	var timestamp string
	if evalMatches, ok := payload["evalMatches"].([]interface{}); ok && len(evalMatches) > 0 {
		if firstMatch, ok := evalMatches[0].(map[string]interface{}); ok {
			if t, ok := firstMatch["time"].(string); ok {
				timestamp = t
			}
		}
	}
	if timestamp == "" {
		if t, ok := payload["timestamp"].(string); ok {
			timestamp = t
		}
	}
	if timestamp == "" {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		log.Error().Msg("Missing timestamp in grafana webhook payload")
		http.Error(w, "Missing timestamp", http.StatusBadRequest)
		return
	}

	var service string
	if tags, ok := payload["tags"].(map[string]interface{}); ok {
		if s, ok := tags["service"].(string); ok {
			service = s
		}
	}
	if service == "" {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		log.Error().Msg("Missing service in grafana webhook payload tags")
		http.Error(w, "Missing service", http.StatusBadRequest)
		return
	}

	var title string
	if t, ok := payload["ruleName"].(string); ok {
		title = t
	}
	if title == "" {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		log.Error().Msg("Missing ruleName in grafana webhook payload")
		http.Error(w, "Missing ruleName", http.StatusBadRequest)
		return
	}

	var severity string

	event := models.CommonEvent{
		ID:        eventID,
		Source:    source,
		Type:      eventType,
		Timestamp: timestamp,
		Service:   service,
		Title:     title,
		Severity:  severity,
		Labels:    make(map[string]string),
		RawPayload: payload,
	}

	// Copy labels from tags if present
	if tags, ok := payload["tags"].(map[string]interface{}); ok {
		for k, v := range tags {
			if str, ok := v.(string); ok {
				event.Labels[k] = str
			}
		}
	}

	// Insert into events table with metrics
	dbStart := time.Now()
	_, err := h.db.ExecContext(r.Context(),
		`INSERT INTO events (id, source, type, timestamp, service, environment, severity, title, status, labels, raw_payload) 
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		event.ID, event.Source, event.Type, event.Timestamp, event.Service, "", event.Severity, event.Title, "firing", event.Labels, event.RawPayload,
	)
	h.metrics.DBWriteDuration.Observe(time.Since(dbStart).Seconds())

	if err != nil {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "500").Inc()
		log.Error().Err(err).Str("event_id", eventID).Msg("Error inserting event")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.metrics.EventsProcessedTotal.Inc()
	h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "202").Inc()
	h.metrics.WebhookRequestDuration.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())

	log.Info().
		Str("event_id", eventID).
		Str("service", service).
		Str("title", title).
		Msg("Grafana webhook processed successfully")

	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(models.WebhookResponse{Status: "accepted", EventID: event.ID}); err != nil {
		log.Error().Err(err).Msg("Failed to encode webhook response")
	}
}

// GenericWebhook processes a generic JSON webhook and normalizes it
func (h *Handler) GenericWebhook(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	endpoint := "generic"

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		log.Error().Err(err).Msg("Invalid JSON in generic webhook")
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	eventID := uuid.NewString()
	source := "generic"
	eventType := "event"

	var timestamp string
	if t, ok := payload["timestamp"].(string); ok {
		timestamp = t
	}
	if timestamp == "" {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		log.Error().Msg("Missing timestamp in generic webhook payload")
		http.Error(w, "Missing timestamp", http.StatusBadRequest)
		return
	}

	var service string
	if s, ok := payload["service"].(string); ok {
		service = s
	}
	if service == "" {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		log.Error().Msg("Missing service in generic webhook payload")
		http.Error(w, "Missing service", http.StatusBadRequest)
		return
	}

	event := models.CommonEvent{
		ID:        eventID,
		Source:    source,
		Type:      eventType,
		Timestamp: timestamp,
		Service:   service,
		Labels:    make(map[string]string),
		RawPayload: payload,
	}

	// Extract optional fields
	if env, ok := payload["environment"].(string); ok {
		event.Environment = env
	}
	if sev, ok := payload["severity"].(string); ok {
		event.Severity = sev
	}
	if t, ok := payload["title"].(string); ok {
		event.Title = t
	}
	if st, ok := payload["status"].(string); ok {
		event.Status = st
	} else {
		event.Status = "firing"
	}
	if lbls, ok := payload["labels"].(map[string]interface{}); ok {
		for k, v := range lbls {
			if str, ok := v.(string); ok {
				event.Labels[k] = str
			}
		}
	}

	dbStart := time.Now()
	_, err := h.db.ExecContext(r.Context(),
		`INSERT INTO events (id, source, type, timestamp, service, environment, severity, title, status, labels, raw_payload) 
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		event.ID, event.Source, event.Type, event.Timestamp, event.Service, event.Environment, event.Severity, event.Title, event.Status, event.Labels, event.RawPayload,
	)
	h.metrics.DBWriteDuration.Observe(time.Since(dbStart).Seconds())

	if err != nil {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "500").Inc()
		log.Error().Err(err).Str("event_id", eventID).Msg("Error inserting event")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.metrics.EventsProcessedTotal.Inc()
	h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "202").Inc()
	h.metrics.WebhookRequestDuration.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())

	log.Info().
		Str("event_id", eventID).
		Str("service", service).
		Str("title", event.Title).
		Msg("Generic webhook processed successfully")

	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(models.WebhookResponse{Status: "accepted", EventID: event.ID}); err != nil {
		log.Error().Err(err).Msg("Failed to encode webhook response")
	}
}

// HealthHandler handles health check requests
func (h *Handler) HealthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		h.metrics.DBConnectionErrors.Inc()
		log.Error().Err(err).Msg("Health check failed: database connection")
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(models.HealthResponse{
			Status: "unhealthy",
			Error:  "database connection failed",
		}); err != nil {
			log.Error().Err(err).Msg("Failed to encode health response")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(models.HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		log.Error().Err(err).Msg("Failed to encode health response")
	}
}

// ReadyHandler handles readiness check requests
func (h *Handler) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		log.Error().Err(err).Msg("Readiness check failed: database not ready")
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(models.ReadyResponse{
			Status: "not ready",
			Error:  "database not ready",
		}); err != nil {
			log.Error().Err(err).Msg("Failed to encode ready response")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(models.ReadyResponse{Status: "ready"}); err != nil {
		log.Error().Err(err).Msg("Failed to encode ready response")
	}
}

// MetricsHandler returns the Prometheus metrics handler
func (h *Handler) MetricsHandler() http.Handler {
	return promhttp.Handler()
}