package handlers_test

import (
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

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE source = 'grafana'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// The webhook handler forwards the alert itself (fire-and-forget); no
	// correlator link is needed because the payload is event-sourced.
	select {
	case receivedPayload := <-received:
		assert.Equal(t, "High CPU Usage", receivedPayload["title"])
		assert.Equal(t, "critical", receivedPayload["severity"])
		assert.Equal(t, "production", receivedPayload["environment"])
		assert.Equal(t, 1, int(receivedPayload["contract_version"].(float64)))
		// No incident exists yet at alert time.
		assert.Nil(t, receivedPayload["incident_id"])
		// A dedup key must be present so repeat fires collapse to one alert.
		assert.NotEmpty(t, receivedPayload["dedup_key"])
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

	select {
	case receivedPayload := <-received:
		assert.Equal(t, "High CPU Usage", receivedPayload["title"])
		assert.Equal(t, "critical", receivedPayload["severity"])
		assert.Equal(t, "production", receivedPayload["environment"])
		assert.Nil(t, receivedPayload["incident_id"], "alert fires before correlation creates an incident")
		assert.NotEmpty(t, receivedPayload["dedup_key"])
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

	// The webhook handler's own forward must be gated out: no HTTP call.
	select {
	case <-called:
		t.Fatal("reporter was called for a below-threshold severity")
	case <-time.After(500 * time.Millisecond):
		// Expected: the gate dropped it before any lookup or HTTP call.
	}
}

// TestGrafanaWebhook_NamesTheTargetNotTheSender is the regression test for
// ambiguous alerts. Grafana's default alerting payload has no service tag; it
// names the target in `instance` and the scrape job in `job`. The old code
// defaulted the service to the literal "grafana", so every alert read
// "Services: grafana" and never said what was actually down.
func TestGrafanaWebhook_NamesTheTargetNotTheSender(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	received := make(chan map[string]interface{}, 1)
	reporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		received <- payload
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"sent":true}`))
	}))
	defer reporter.Close()

	th := newTestHandlerWithAlerts(t, db, reporter.URL)

	// Grafana default alerting format: no tags.service anywhere.
	payload := map[string]interface{}{
		"alerts": []interface{}{
			map[string]interface{}{
				"status":      "firing",
				"fingerprint": "24188862b3fad691",
				"startsAt":    time.Now().UTC().Format(time.RFC3339),
				"labels": map[string]interface{}{
					"alertname": "Monitoring Target Down",
					"severity":  "critical",
					"instance":  "last-labs",
					"job":       "docker_cadvisor",
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	w := th.makeRequest("POST", "/api/v1/webhooks/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

	select {
	case p := <-received:
		assert.Equal(t, "last-labs", p["target"], "alert must name the target")
		assert.Equal(t, "docker_cadvisor", p["job"], "alert must name the scrape job")
		assert.NotEqual(t, "grafana", p["services"], "the sender is not the affected service")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the reporter to receive the alert")
	}
}

// TestGenericWebhookRequiresService pins where the service requirement lives.
// Grafana's default alerting payload names no service, so the shared validator
// cannot require one; generic callers control their own payload shape, so the
// generic endpoint enforces it at its boundary. The check runs before any
// database access, so this needs no live PostgreSQL.
func TestGenericWebhookRequiresService(t *testing.T) {
	th := newTestHandler(t, nil)

	body, err := json.Marshal(map[string]interface{}{
		"schema_version": 1,
		"type":           "event",
		"occurred_at":    time.Now().UTC().Format(time.RFC3339),
		"environment":    "production",
		"title":          "Deploy finished",
		"severity":       "info",
		"status":         "informational",
	})
	require.NoError(t, err)

	w := th.makeRequest("POST", "/api/v1/webhooks/generic", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())

	var resp struct {
		Error struct {
			Details []struct {
				Field string `json:"field"`
			} `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Error.Details, 1)
	assert.Equal(t, "service", resp.Error.Details[0].Field)
}
