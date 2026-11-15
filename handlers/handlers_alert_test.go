package handlers_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Pamawas/pamawas-ingest/config"
	"github.com/Pamawas/pamawas-ingest/handlers"
	"github.com/Pamawas/pamawas-ingest/metrics"
	"github.com/Pamawas/pamawas-ingest/middleware"
	"github.com/Pamawas/pamawas-ingest/models"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"
)

// newTestHandlerWithAlerts builds a test handler wired with an AlertClient
// that posts to the given reporter test server, using the same router as main.go.
func newTestHandlerWithAlerts(t *testing.T, db *sql.DB, reporterURL string) *TestHandler { //nolint:unused
	cfg := config.Config{
		DatabaseURL:                    "",
		Port:                           "8080",
		LogLevel:                       "debug",
		Environment:                    "test",
		WebhookToken:                   "test-token",
		MaxBodyBytes:                   1048576,
		TestMode:                       true,
		ReporterURL:                    reporterURL,
		RateLimitRPS:                   1000,
		RateLimitBurst:                 2000,
		WebhookRateLimitRPS:            1000,
		WebhookRateLimitBurst:          2000,
		CircuitBreakerFailureThreshold: 3,
		CircuitBreakerResetTimeout:     30 * time.Second,
		HighSeverityThreshold:          "high",
	}

	reg := prometheus.NewRegistry()
	m := metrics.NewMetricsWithRegistry(reg)
	h := handlers.NewHandler(db, cfg, m)

	r := mux.NewRouter()
	r.Use(middleware.LoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.ErrorLoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.BodyLimitMiddleware(cfg.MaxBodyBytes))
	r.Use(middleware.RateLimit("pamawas-ingest", map[string]middleware.RouteLimit{
		"/api/v1/webhooks/grafana":          {RPS: cfg.WebhookRateLimitRPS, Burst: cfg.WebhookRateLimitBurst},
		"/api/v1/webhooks/generic":          {RPS: cfg.WebhookRateLimitRPS, Burst: cfg.WebhookRateLimitBurst},
		"/api/v1/retry-stalled-idempotency": {RPS: cfg.RateLimitRPS, Burst: cfg.RateLimitBurst},
	}))

	v1 := r.PathPrefix("/api/v1").Subrouter()
	v1.HandleFunc("/webhooks/grafana", h.GrafanaWebhook).Methods("POST")
	v1.HandleFunc("/webhooks/generic", h.GenericWebhook).Methods("POST")
	v1.HandleFunc("/retry-stalled-idempotency", h.RecoverStalledIdempotencyHandler).Methods("POST")

	r.HandleFunc("/healthz", h.HealthHandler).Methods("GET")
	r.HandleFunc("/ready", h.ReadyHandler).Methods("GET")
	r.Handle("/metrics", h.MetricsHandler())

	return &TestHandler{Handler: h, db: db, router: r}
}

// TestGrafanaWebhook_ResolvedForwardsAlert verifies end-to-end: a resolved
// Grafana webhook triggers a POST to the reporter's /api/v1/alerts/resolved.
func TestGrafanaWebhook_ResolvedForwardsAlert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	received := make(chan map[string]interface{}, 1)
	reporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/alerts/resolved", r.URL.Path)
		var payload map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		received <- payload
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"incident_id":"inc_resolved_1","sent":true,"message":"ok"}`))
	}))
	defer reporter.Close()

	th := newTestHandlerWithAlerts(t, db, reporter.URL)

	// Insert an incident (linked to the event via incident_events after it exists).
	_, err := db.ExecContext(t.Context(), `INSERT INTO incidents (id, title, status, started_at, last_event_at, resolved_at, severity, environment, affected_services, correlation_policy, correlation_version) VALUES ($1,'High CPU','resolved',$2,$3,$4,'critical','production','{}','policy',1)`, "inc_resolved_1", time.Now().Add(-time.Hour), time.Now(), time.Now())
	require.NoError(t, err)

	payload := map[string]interface{}{
		"ruleName":  "High CPU Usage",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"status":    "resolved",
		"state":     "ok",
		"tags": map[string]interface{}{
			"service":   "payment-api",
			"severity":  "critical",
			"namespace": "payments",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := th.makeRequest("POST", "/api/v1/webhooks/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusAccepted, w.Code)

	// Verify the event was inserted and capture its ID.
	var resp struct {
		Data struct {
			EventID string `json:"event_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Data.EventID)
	eventID := resp.Data.EventID

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE source = 'grafana'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Link the event to the incident (as the correlator would).
	_, err = db.ExecContext(t.Context(), `INSERT INTO incident_events (incident_id, event_id, correlation_reason, policy_version) VALUES ($1, $2, 'resolution_of_open_incident', 1)`, "inc_resolved_1", eventID)
	require.NoError(t, err)

	// Forward the alert to the reporter directly (synchronously for the test).
	th.ForwardEventToReporter(context.Background(), eventID, &models.WebhookRequest{Status: "resolved"})

	// Verify the reporter received the resolved-alert POST.
	select {
	case receivedPayload := <-received:
		assert.Equal(t, "inc_resolved_1", receivedPayload["incident_id"])
		assert.Equal(t, 1, int(receivedPayload["contract_version"].(float64)))
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for reporter to receive resolved alert")
	}
}

// TestGrafanaWebhook_FiringForwardsAlert verifies a firing webhook forwards
// the immediate alert to the reporter's /api/v1/alerts endpoint.
func TestGrafanaWebhook_FiringForwardsAlert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	received := make(chan map[string]interface{}, 1)
	reporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/alerts", r.URL.Path)
		var payload map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		received <- payload
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"incident_id":"inc_firing_1","sent":true,"message":"ok"}`))
	}))
	defer reporter.Close()

	th := newTestHandlerWithAlerts(t, db, reporter.URL)

	_, err := db.ExecContext(t.Context(), `INSERT INTO incidents (id, title, status, started_at, last_event_at, resolved_at, severity, environment, affected_services, correlation_policy, correlation_version) VALUES ($1,'High CPU','open',$2,$2,NULL,'critical','production','{}','policy',1)`, "inc_firing_1", time.Now())
	require.NoError(t, err)

	payload := map[string]interface{}{
		"ruleName":  "High CPU Usage",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"status":    "firing",
		"state":     "alerting",
		"tags": map[string]interface{}{
			"service":   "payment-api",
			"severity":  "critical",
			"namespace": "payments",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := th.makeRequest("POST", "/api/v1/webhooks/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusAccepted, w.Code)

	var resp struct {
		Data struct {
			EventID string `json:"event_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	eventID := resp.Data.EventID
	require.NotEmpty(t, eventID)

	_, err = db.ExecContext(t.Context(), `INSERT INTO incident_events (incident_id, event_id, correlation_reason, policy_version) VALUES ($1, $2, 'new_incident', 1)`, "inc_firing_1", eventID)
	require.NoError(t, err)

	th.ForwardEventToReporter(context.Background(), eventID, &models.WebhookRequest{Status: "firing"})

	select {
	case receivedPayload := <-received:
		assert.Equal(t, "inc_firing_1", receivedPayload["incident_id"])
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for reporter to receive firing alert")
	}
}

// TestGrafanaWebhook_LowSeverityDoesNotForwardAlert verifies the env-driven
// severity gate: a firing event below HIGH_SEVERITY_THRESHOLD must not reach
// the reporter at all (no wasted HTTP call), while high/critical does.
func TestGrafanaWebhook_LowSeverityDoesNotForwardAlert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	called := make(chan struct{}, 1)
	reporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"sent":true}`))
	}))
	defer reporter.Close()

	th := newTestHandlerWithAlerts(t, db, reporter.URL)

	payload := map[string]interface{}{
		"ruleName":  "Disk usage warning",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"status":    "firing",
		"state":     "alerting",
		"tags": map[string]interface{}{
			"service":   "payment-api",
			"severity":  "warning",
			"namespace": "payments",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := th.makeRequest("POST", "/api/v1/webhooks/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusAccepted, w.Code)

	var resp struct {
		Data struct {
			EventID string `json:"event_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Data.EventID)

	// Even with the incident link present, the gate must stop the forward.
	_, err = db.ExecContext(t.Context(), `INSERT INTO incidents (id, title, status, started_at, last_event_at, resolved_at, severity, environment, affected_services, correlation_policy, correlation_version) VALUES ($1,'Disk usage warning','open',$2,$2,NULL,'warning','production','{}','policy',1)`, "inc_warning_1", time.Now())
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO incident_events (incident_id, event_id, correlation_reason, policy_version) VALUES ($1, $2, 'new_incident', 1)`, "inc_warning_1", resp.Data.EventID)
	require.NoError(t, err)

	th.ForwardEventToReporter(context.Background(), resp.Data.EventID, &models.WebhookRequest{Status: "firing", Severity: "warning"})

	select {
	case <-called:
		t.Fatal("reporter was called for a below-threshold severity")
	case <-time.After(500 * time.Millisecond):
		// Expected: the gate dropped it before any lookup or HTTP call.
	}
}
