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

// newTestHandlerWithResolver builds a test handler wired with a ResolveNotifier
// that posts to the given reporter test server, using the same router as main.go.
func newTestHandlerWithResolver(t *testing.T, db *sql.DB, reporterURL string) *TestHandler {
	cfg := config.Config{
		DatabaseURL:  "",
		Port:         "8080",
		LogLevel:     "debug",
		Environment:  "test",
		WebhookToken: "test-token",
		MaxBodyBytes: 1048576,
		TestMode:     true,
		ReporterURL:  reporterURL,
	}

	reg := prometheus.NewRegistry()
	m := metrics.NewMetricsWithRegistry(reg)
	h := handlers.NewHandler(db, cfg, m)

	r := mux.NewRouter()
	r.Use(middleware.LoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.ErrorLoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.BodyLimitMiddleware(cfg.MaxBodyBytes))

	v1 := r.PathPrefix("/api/v1").Subrouter()
	v1.HandleFunc("/webhooks/grafana", h.GrafanaWebhook).Methods("POST")
	v1.HandleFunc("/webhooks/generic", h.GenericWebhook).Methods("POST")
	v1.HandleFunc("/retry-stalled-idempotency", h.RecoverStalledIdempotencyHandler).Methods("POST")

	r.HandleFunc("/healthz", h.HealthHandler).Methods("GET")
	r.HandleFunc("/ready", h.ReadyHandler).Methods("GET")
	r.Handle("/metrics", h.MetricsHandler())

	return &TestHandler{Handler: h, db: db, router: r}
}

// TestGrafanaWebhook_ResolvedForwardsToReporter verifies end-to-end: a
// resolved Grafana webhook triggers a POST to the reporter's
// /api/v1/resolve-notification endpoint.
func TestGrafanaWebhook_ResolvedForwardsToReporter(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	received := make(chan map[string]interface{}, 1)
	reporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/resolve-notification", r.URL.Path)
		var payload map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		received <- payload
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"incident_id":"inc_resolved_1","sent":true,"message":"ok"}`))
	}))
	defer reporter.Close()

	th := newTestHandlerWithResolver(t, db, reporter.URL)

	// Insert an incident linked to the event via incident_events.
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

	// Verify event was inserted.
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE source = 'grafana'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify the reporter received the resolve-notification POST.
	select {
	case receivedPayload := <-received:
		assert.Equal(t, "inc_resolved_1", receivedPayload["incident_id"])
		assert.Equal(t, 1, int(receivedPayload["contract_version"].(float64)))
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for reporter to receive resolve notification")
	}
}
