package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/Pamawas/pamawas-ingest/config"
	"github.com/Pamawas/pamawas-ingest/metrics"
	"github.com/Pamawas/pamawas-ingest/models"
	"github.com/Pamawas/pamawas-ingest/utils"
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

// writeErrorResponse writes a standardized error response
func writeErrorResponse(w http.ResponseWriter, requestID string, statusCode int, code, message string, details []models.ErrorDetail) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	resp := models.WebhookResponse{
		RequestID: requestID,
		Error: &models.ErrorResponse{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Error().Err(err).Str("request_id", requestID).Msg("Failed to encode error response")
	}
}

// writeSuccessResponse writes a successful webhook response
func writeSuccessResponse(w http.ResponseWriter, requestID string, eventID string, duplicate bool) {
	w.Header().Set("Content-Type", "application/json")
	statusCode := http.StatusAccepted
	if duplicate {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
	resp := models.WebhookResponse{
		RequestID: requestID,
	}
	resp.Data.EventID = eventID
	resp.Data.Status = "accepted"
	resp.Data.Duplicate = duplicate
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Error().Err(err).Str("request_id", requestID).Msg("Failed to encode success response")
	}
}

// validateContentType checks that the content type is application/json
func validateContentType(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/json")
}

// validateAuth checks webhook token authentication
func (h *Handler) validateAuth(r *http.Request, _ string) bool {
	if h.cfg.TestMode {
		return true
	}
	if h.cfg.WebhookToken == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	expected := "Bearer " + h.cfg.WebhookToken
	return auth == expected
}

// processEvent handles the common event processing logic
func (h *Handler) processEvent(ctx context.Context, requestID, source string, req *models.WebhookRequest, idempotencyKey string) (string, bool, error) {
	endpoint := source
	start := time.Now()

	// Validate request
	if details := models.ValidateWebhookRequest(req); len(details) > 0 {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "422").Inc()
		return "", false, &models.ValidationError{Details: details}
	}

	// Generate event ID
	eventID := uuid.NewString()

	// Generate fingerprint if no source_event_id
	fingerprint := ""
	if req.SourceEventID == "" {
		fingerprint = utils.GenerateFingerprint(req)
	}

	// Determine idempotency key
	var ikey string
	if idempotencyKey != "" {
		ikey = idempotencyKey
	} else if req.SourceEventID != "" {
		ikey = utils.GenerateIdempotencyKey(source, req.SourceEventID)
	} else {
		ikey = utils.GenerateIdempotencyKeyFromFingerprint(source, fingerprint)
	}

	// Check idempotency
	existingEventID, duplicate, conflict, err := utils.CheckIdempotency(h.db, "ingest", source, ikey, req)
	if err != nil {
		if utils.IsConflict(err) {
			h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "409").Inc()
			return "", false, err
		}
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "500").Inc()
		log.Error().Err(err).Str("request_id", requestID).Str("event_id", eventID).Msg("Error checking idempotency")
		return "", false, err
	}

	if duplicate {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "200").Inc()
		h.metrics.EventsProcessedTotal.Inc()
		h.metrics.WebhookRequestDuration.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())
		return existingEventID, true, nil
	}

	if conflict {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "409").Inc()
		return "", false, utils.ErrStillProcessing // will be converted to 409
	}

	// Convert to common event
	event := req.ToCommonEvent(source, eventID, fingerprint)

	// Insert into events table with metrics
	dbStart := time.Now()
	labelsJSON, err := json.Marshal(event.Labels)
	if err != nil {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "500").Inc()
		log.Error().Err(err).Str("event_id", eventID).Msg("Error marshaling labels")
		return "", false, err
	}
	rawPayloadJSON, err := json.Marshal(event.RawPayload)
	if err != nil {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "500").Inc()
		log.Error().Err(err).Str("event_id", eventID).Msg("Error marshaling raw payload")
		return "", false, err
	}
	_, err = h.db.ExecContext(ctx,
		`INSERT INTO events (id, source, source_event_id, fingerprint, type, occurred_at, service, environment, severity, title, status, labels, raw_payload, schema_version)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		event.ID, event.Source, event.SourceEventID, event.Fingerprint, event.Type, event.OccurredAt,
		event.Service, event.Environment, event.Severity, event.Title, event.Status, labelsJSON, rawPayloadJSON, event.SchemaVersion,
	)
	h.metrics.DBWriteDuration.Observe(time.Since(dbStart).Seconds())

	if err != nil {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "500").Inc()
		log.Error().Err(err).Str("event_id", eventID).Msg("Error inserting event")
		return "", false, err
	}

	// Mark idempotency as completed
	if err := utils.CompleteIdempotency(h.db, "ingest", source, ikey, eventID); err != nil {
		log.Error().Err(err).Str("event_id", eventID).Msg("Failed to complete idempotency record")
		// Non-fatal, event was inserted
	}

	h.metrics.EventsProcessedTotal.Inc()
	h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "202").Inc()
	h.metrics.WebhookRequestDuration.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())

	log.Info().
		Str("event_id", eventID).
		Str("service", event.Service).
		Str("title", event.Title).
		Bool("duplicate", false).
		Msg("Webhook processed successfully")

	return eventID, false, nil
}

// GrafanaWebhook handles Grafana alert webhook payload
func (h *Handler) GrafanaWebhook(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()[:8]
	endpoint := "grafana"

	// Check content type
	if !validateContentType(r) {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "415").Inc()
		writeErrorResponse(w, requestID, http.StatusUnsupportedMediaType, "invalid_request", "Content-Type must be application/json", nil)
		return
	}

	// Check auth
	if !h.validateAuth(r, "grafana") {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "401").Inc()
		writeErrorResponse(w, requestID, http.StatusUnauthorized, "unauthorized", "Invalid or missing authentication", nil)
		return
	}

	// Check body size
	if r.ContentLength > h.cfg.MaxBodyBytes {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "413").Inc()
		writeErrorResponse(w, requestID, http.StatusRequestEntityTooLarge, "invalid_request", "Request body too large", nil)
		return
	}

	// Parse JSON with limit
	var payload map[string]interface{}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		writeErrorResponse(w, requestID, http.StatusBadRequest, "invalid_request", "Invalid JSON", nil)
		return
	}

	// Check for trailing data
	if decoder.More() {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		writeErrorResponse(w, requestID, http.StatusBadRequest, "invalid_request", "Trailing data after JSON object", nil)
		return
	}

	// Convert to normalized request
	grafanaReq := &models.GrafanaWebhookRequest{
		RawPayload: payload,
	}
	if ruleName, ok := payload["ruleName"].(string); ok {
		grafanaReq.RuleName = ruleName
	}
	if timestamp, ok := payload["timestamp"].(string); ok {
		grafanaReq.Timestamp = timestamp
	}
	if evalMatches, ok := payload["evalMatches"].([]interface{}); ok {
		for _, em := range evalMatches {
			if emMap, ok := em.(map[string]interface{}); ok {
				match := models.EvalMatch{}
				if t, ok := emMap["time"].(string); ok {
					match.Time = t
				}
				if v, ok := emMap["value"].(string); ok {
					match.Value = v
				}
				if metric, ok := emMap["metric"].(map[string]interface{}); ok {
					match.Metric = make(map[string]string)
					for k, v := range metric {
						if str, ok := v.(string); ok {
							match.Metric[k] = str
						}
					}
				}
				grafanaReq.EvalMatches = append(grafanaReq.EvalMatches, match)
			}
		}
	}
	if tags, ok := payload["tags"].(map[string]interface{}); ok {
		grafanaReq.Tags = make(map[string]string)
		for k, v := range tags {
			if str, ok := v.(string); ok {
				grafanaReq.Tags[k] = str
			}
		}
	}

	webhookReq := grafanaReq.ToWebhookRequest()
	if webhookReq.OccurredAt == "" {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "422").Inc()
		writeErrorResponse(w, requestID, http.StatusUnprocessableEntity, "invalid_request", "Missing timestamp",
			[]models.ErrorDetail{{Field: "timestamp", Reason: "required"}})
		return
	}
	if webhookReq.Service == "" {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "422").Inc()
		writeErrorResponse(w, requestID, http.StatusUnprocessableEntity, "invalid_request", "Missing service",
			[]models.ErrorDetail{{Field: "service", Reason: "required (from tags.service)"}})
		return
	}

	// Get idempotency key from header
	idempotencyKey := r.Header.Get("X-Idempotency-Key")

	// Process event
	eventID, duplicate, err := h.processEvent(r.Context(), requestID, "grafana", webhookReq, idempotencyKey)
	if err != nil {
		if err == utils.ErrStillProcessing || strings.Contains(err.Error(), "conflict") {
			writeErrorResponse(w, requestID, http.StatusConflict, "conflict", "Idempotency key conflict: same key with different request",
				[]models.ErrorDetail{{Field: "X-Idempotency-Key", Reason: "conflict: same key with different request"}})
			return
		}
		if validationErr, ok := err.(*models.ValidationError); ok {
			writeErrorResponse(w, requestID, http.StatusUnprocessableEntity, "invalid_request", "request validation failed", validationErr.Details)
			return
		}
		writeErrorResponse(w, requestID, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	writeSuccessResponse(w, requestID, eventID, duplicate)
}

// GenericWebhook processes a generic JSON webhook and normalizes it
func (h *Handler) GenericWebhook(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()[:8]
	endpoint := "generic"

	// Check content type
	if !validateContentType(r) {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "415").Inc()
		writeErrorResponse(w, requestID, http.StatusUnsupportedMediaType, "invalid_request", "Content-Type must be application/json", nil)
		return
	}

	// Check auth
	if !h.validateAuth(r, "generic") {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "401").Inc()
		writeErrorResponse(w, requestID, http.StatusUnauthorized, "unauthorized", "Invalid or missing authentication", nil)
		return
	}

	// Check body size
	if r.ContentLength > h.cfg.MaxBodyBytes {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "413").Inc()
		writeErrorResponse(w, requestID, http.StatusRequestEntityTooLarge, "invalid_request", "Request body too large", nil)
		return
	}

	// Parse JSON with limit
	var req models.WebhookRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		writeErrorResponse(w, requestID, http.StatusBadRequest, "invalid_request", "Invalid JSON", nil)
		return
	}

	// Check for trailing data
	if decoder.More() {
		h.metrics.WebhookRequestsTotal.WithLabelValues(endpoint, "400").Inc()
		writeErrorResponse(w, requestID, http.StatusBadRequest, "invalid_request", "Trailing data after JSON object", nil)
		return
	}

	// Get idempotency key from header
	idempotencyKey := r.Header.Get("X-Idempotency-Key")

	// Process event
	eventID, duplicate, err := h.processEvent(r.Context(), requestID, "generic", &req, idempotencyKey)
	if err != nil {
		if err == utils.ErrStillProcessing || strings.Contains(err.Error(), "conflict") {
			writeErrorResponse(w, requestID, http.StatusConflict, "conflict", "Idempotency key conflict: same key with different request",
				[]models.ErrorDetail{{Field: "X-Idempotency-Key", Reason: "conflict: same key with different request"}})
			return
		}
		if validationErr, ok := err.(*models.ValidationError); ok {
			writeErrorResponse(w, requestID, http.StatusUnprocessableEntity, "invalid_request", "request validation failed", validationErr.Details)
			return
		}
		writeErrorResponse(w, requestID, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	writeSuccessResponse(w, requestID, eventID, duplicate)
}

// HealthHandler handles health check requests
func (h *Handler) HealthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		h.metrics.DBConnectionErrors.Inc()
		log.Error().Err(err).Msg("Health check failed: database connection")
		w.Header().Set("Content-Type", "application/json")
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
		w.Header().Set("Content-Type", "application/json")
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
