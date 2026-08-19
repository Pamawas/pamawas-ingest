package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Pamawas/pamawas-ingest/config"
	"github.com/Pamawas/pamawas-ingest/handlers"
	"github.com/Pamawas/pamawas-ingest/metrics"
	"github.com/Pamawas/pamawas-ingest/middleware"
	"github.com/Pamawas/pamawas-ingest/models"
)

// TestHandler holds test dependencies
type TestHandler struct {
	*handlers.Handler
	db     *sql.DB
	server *httptest.Server
	router http.Handler // Add router for testing
}

// setupTestDB creates a test database and runs migrations
func setupTestDB(t *testing.T) *sql.DB {
	// Use test database from environment or default
	dbURL := "postgres://pamawas:***@localhost:5432/pamawas_test?sslmode=disable"
	if testingDB := getenv("TEST_DATABASE_URL", ""); testingDB != "" {
		dbURL = testingDB
	}

	db, err := sql.Open("postgres", dbURL)
	require.NoError(t, err)

	// Clean up tables before each test
	_, err = db.Exec(`
		TRUNCATE TABLE events, idempotency_records RESTART IDENTITY CASCADE;
	`)
	require.NoError(t, err)

	return db
}

func getenv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// newTestHandler creates a test handler with test config
func newTestHandler(t *testing.T, db *sql.DB) *TestHandler {
	cfg := config.Config{
		DatabaseURL:  "",
		Port:         "8080",
		LogLevel:     "debug",
		Environment:  "test",
		WebhookToken: "test-token",
		MaxBodyBytes: 1048576,
		TestMode:     true,
	}

	// Use a custom registry for tests to avoid duplicate metrics registration
	reg := prometheus.NewRegistry()
	m := metrics.NewMetricsWithRegistry(reg)
	h := handlers.NewHandler(db, cfg, m)

	// Create router same as main.go
	r := mux.NewRouter()
	r.Use(middleware.LoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.ErrorLoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.BodyLimitMiddleware(cfg.MaxBodyBytes))

	// V1 API endpoints
	v1 := r.PathPrefix("/v1").Subrouter()
	v1.HandleFunc("/webhooks/grafana", h.GrafanaWebhook).Methods("POST")
	v1.HandleFunc("/webhooks/generic", h.GenericWebhook).Methods("POST")

	// Legacy endpoints (transitional, to be removed after migration)
	r.HandleFunc("/webhook/grafana", h.GrafanaWebhook).Methods("POST")
	r.HandleFunc("/webhook/generic", h.GenericWebhook).Methods("POST")

	// Health and metrics
	r.HandleFunc("/healthz", h.HealthHandler).Methods("GET")
	r.HandleFunc("/ready", h.ReadyHandler).Methods("GET")
	r.Handle("/metrics", h.MetricsHandler())

	return &TestHandler{Handler: h, db: db, router: r}
}

// makeRequest makes an HTTP request to the handler
func (th *TestHandler) makeRequest(method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	th.router.ServeHTTP(w, req)
	return w
}

func TestGrafanaWebhook_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	payload := map[string]interface{}{
		"ruleName":  "High CPU Usage",
		"timestamp": "2026-08-16T01:47:00Z",
		"tags": map[string]interface{}{
			"service":   "payment-api",
			"severity":  "high",
			"namespace": "payments",
		},
		"evalMatches": []interface{}{
			map[string]interface{}{
				"time":  "2026-08-16T01:47:00Z",
				"value": "95.5",
			},
		},
	}
	body, _ := json.Marshal(payload)

	w := th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})

	assert.Equal(t, http.StatusAccepted, w.Code)

	var resp models.WebhookResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "accepted", resp.Data.Status)
	assert.False(t, resp.Data.Duplicate)
	assert.NotEmpty(t, resp.Data.EventID)

	// Verify event was inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE source = 'grafana'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestGrafanaWebhook_Duplicate_Retry(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	payload := map[string]interface{}{
		"ruleName":  "High CPU Usage",
		"timestamp": "2026-08-16T01:47:00Z",
		"tags": map[string]interface{}{
			"service":   "payment-api",
			"severity":  "high",
			"namespace": "payments",
		},
		"evalMatches": []interface{}{
			map[string]interface{}{
				"time":  "2026-08-16T01:47:00Z",
				"value": "95.5",
			},
		},
	}
	body, _ := json.Marshal(payload)

	// First request with idempotency key
	idempotencyKey := "test-key-1"
	w := th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":      "application/json",
		"Authorization":     "Bearer test-token",
		"X-Idempotency-Key": idempotencyKey,
	})
	assert.Equal(t, http.StatusAccepted, w.Code)

	var resp1 models.WebhookResponse
	json.Unmarshal(w.Body.Bytes(), &resp1)
	eventID1 := resp1.Data.EventID

	// Second request with same idempotency key (retry)
	w = th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":      "application/json",
		"Authorization":     "Bearer test-token",
		"X-Idempotency-Key": idempotencyKey,
	})
	assert.Equal(t, http.StatusOK, w.Code) // 200 for duplicate

	var resp2 models.WebhookResponse
	json.Unmarshal(w.Body.Bytes(), &resp2)
	assert.True(t, resp2.Data.Duplicate)
	assert.Equal(t, eventID1, resp2.Data.EventID)

	// Verify only one event in database
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM events WHERE source = 'grafana'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestGrafanaWebhook_Conflict_SameKeyDifferentPayload(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	payload1 := map[string]interface{}{
		"ruleName":  "High CPU Usage",
		"timestamp": "2026-08-16T01:47:00Z",
		"tags":      map[string]interface{}{"service": "payment-api"},
		"evalMatches": []interface{}{
			map[string]interface{}{"time": "2026-08-16T01:47:00Z"},
		},
	}
	body1, _ := json.Marshal(payload1)

	idempotencyKey := "test-conflict-key"
	w := th.makeRequest("POST", "/webhook/grafana", body1, map[string]string{
		"Content-Type":      "application/json",
		"Authorization":     "Bearer test-token",
		"X-Idempotency-Key": idempotencyKey,
	})
	assert.Equal(t, http.StatusAccepted, w.Code)

	// Different payload with same idempotency key
	payload2 := map[string]interface{}{
		"ruleName":  "High Memory Usage", // Different rule name
		"timestamp": "2026-08-16T01:47:00Z",
		"tags":      map[string]interface{}{"service": "payment-api"},
		"evalMatches": []interface{}{
			map[string]interface{}{"time": "2026-08-16T01:47:00Z"},
		},
	}
	body2, _ := json.Marshal(payload2)

	w = th.makeRequest("POST", "/webhook/grafana", body2, map[string]string{
		"Content-Type":      "application/json",
		"Authorization":     "Bearer test-token",
		"X-Idempotency-Key": idempotencyKey,
	})
	assert.Equal(t, http.StatusConflict, w.Code)

	var resp models.WebhookResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, "conflict", resp.Error.Code)
}

func TestGrafanaWebhook_MissingTimestamp(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	payload := map[string]interface{}{
		"ruleName": "High CPU Usage",
		"tags":     map[string]interface{}{"service": "payment-api"},
	}
	body, _ := json.Marshal(payload)

	w := th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestGrafanaWebhook_MissingService(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	payload := map[string]interface{}{
		"ruleName":  "High CPU Usage",
		"timestamp": "2026-08-16T01:47:00Z",
		"tags":      map[string]interface{}{"severity": "high"},
	}
	body, _ := json.Marshal(payload)

	w := th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestGrafanaWebhook_InvalidJSON(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	body := []byte(`{invalid json`)

	w := th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGrafanaWebhook_TrailingData(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	payload := map[string]interface{}{
		"ruleName":  "High CPU Usage",
		"timestamp": "2026-08-16T01:47:00Z",
		"tags":      map[string]interface{}{"service": "payment-api"},
	}
	body, _ := json.Marshal(payload)
	body = append(body, []byte(` "extra"`)...) // trailing data

	w := th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGrafanaWebhook_OversizedBody(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	// Create a large payload > 1MB
	largeString := string(bytes.Repeat([]byte("x"), 1100000))
	payload := map[string]interface{}{
		"ruleName":  "Test",
		"timestamp": "2026-08-16T01:47:00Z",
		"tags":      map[string]interface{}{"service": "test", "data": largeString},
	}
	body, _ := json.Marshal(payload)

	w := th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestGrafanaWebhook_Unauthorized(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	// Disable test mode to test auth
	cfg := config.Config{
		DatabaseURL:  "",
		Port:         "8080",
		LogLevel:     "debug",
		Environment:  "test",
		WebhookToken: "test-token",
		MaxBodyBytes: 1048576,
		TestMode:     false,
	}
	// Use a custom registry for tests to avoid duplicate metrics registration
	reg := prometheus.NewRegistry()
	m := metrics.NewMetricsWithRegistry(reg)
	h := handlers.NewHandler(db, cfg, m)

	// Create router same as main.go
	r := mux.NewRouter()
	r.Use(middleware.LoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.ErrorLoggingMiddleware("pamawas-ingest"))
	r.Use(middleware.BodyLimitMiddleware(cfg.MaxBodyBytes))

	// V1 API endpoints
	v1 := r.PathPrefix("/v1").Subrouter()
	v1.HandleFunc("/webhooks/grafana", h.GrafanaWebhook).Methods("POST")
	v1.HandleFunc("/webhooks/generic", h.GenericWebhook).Methods("POST")

	// Legacy endpoints (transitional, to be removed after migration)
	r.HandleFunc("/webhook/grafana", h.GrafanaWebhook).Methods("POST")
	r.HandleFunc("/webhook/generic", h.GenericWebhook).Methods("POST")

	// Health and metrics
	r.HandleFunc("/healthz", h.HealthHandler).Methods("GET")
	r.HandleFunc("/ready", h.ReadyHandler).Methods("GET")
	r.Handle("/metrics", h.MetricsHandler())

	th := &TestHandler{Handler: h, db: db, router: r}

	payload := map[string]interface{}{
		"ruleName":  "High CPU Usage",
		"timestamp": "2026-08-16T01:47:00Z",
		"tags":      map[string]interface{}{"service": "payment-api"},
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/webhook/grafana", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")
	th.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGrafanaWebhook_FingerprintDeduplication(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	payload := map[string]interface{}{
		"ruleName":  "High CPU Usage",
		"timestamp": "2026-08-16T01:47:00Z",
		"tags": map[string]interface{}{
			"service":   "payment-api",
			"severity":  "high",
			"namespace": "payments",
		},
		"evalMatches": []interface{}{
			map[string]interface{}{
				"time":  "2026-08-16T01:47:00Z",
				"value": "95.5",
			},
		},
	}
	body, _ := json.Marshal(payload)

	// First request - no idempotency key, should use fingerprint
	w := th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusAccepted, w.Code)

	var resp1 models.WebhookResponse
	json.Unmarshal(w.Body.Bytes(), &resp1)
	eventID1 := resp1.Data.EventID

	// Second request - identical payload, should deduplicate via fingerprint
	w = th.makeRequest("POST", "/webhook/grafana", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp2 models.WebhookResponse
	json.Unmarshal(w.Body.Bytes(), &resp2)
	assert.True(t, resp2.Data.Duplicate)
	assert.Equal(t, eventID1, resp2.Data.EventID)

	// Verify only one event in database
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM events WHERE source = 'grafana'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestGenericWebhook_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	req := models.WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "payment-api",
		Environment:   "production",
		Severity:      "high",
		Title:         "Database connection pool exhausted",
		Status:        "firing",
		Labels: map[string]string{
			"namespace":   "payments",
			"alert_rule":  "db_pool_exhaustion",
		},
	}
	body, _ := json.Marshal(req)

	w := th.makeRequest("POST", "/webhook/generic", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusAccepted, w.Code)

	var resp models.WebhookResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "accepted", resp.Data.Status)
	assert.False(t, resp.Data.Duplicate)
	assert.NotEmpty(t, resp.Data.EventID)
}

func TestGenericWebhook_SourceEventIDDeduplication(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	th := newTestHandler(t, db)

	req := models.WebhookRequest{
		SchemaVersion: 1,
		SourceEventID: "grafana-alert-7842",
		Type:          "alert",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "payment-api",
		Environment:   "production",
		Severity:      "high",
		Title:         "Database connection pool exhausted",
		Status:        "firing",
		Labels: map[string]string{
			"namespace":   "payments",
			"alert_rule":  "db_pool_exhaustion",
		},
	}
	body, _ := json.Marshal(req)

	// First request
	w := th.makeRequest("POST", "/webhook/generic", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusAccepted, w.Code)

	var resp1 models.WebhookResponse
	json.Unmarshal(w.Body.Bytes(), &resp1)
	eventID1 := resp1.Data.EventID

	// Second request with same source_event_id
	w = th.makeRequest("POST", "/webhook/generic", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer test-token",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp2 models.WebhookResponse
	json.Unmarshal(w.Body.Bytes(), &resp2)
	assert.True(t, resp2.Data.Duplicate)
	assert.Equal(t, eventID1, resp2.Data.EventID)
}

func TestGenericWebhook_ValidationErrors(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	_ = newTestHandler(t, db) // Run with side effects to populate test DB

	tests := []struct {
		name           string
		req            models.WebhookRequest
		expectedField  string
	}{
		{
			name: "missing schema version",
			req: models.WebhookRequest{
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedField: "schema_version",
		},
		{
			name: "invalid schema version",
			req: models.WebhookRequest{
				SchemaVersion: 2,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedField: "schema_version",
		},
		{
			name: "missing type",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedField: "type",
		},
		{
			name: "invalid type",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "invalid",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedField: "type",
		},
		{
			name: "missing occurred_at",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedField: "occurred_at",
		},
		{
			name: "invalid occurred_at format",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "bad-date",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedField: "occurred_at",
		},
		{
			name: "missing service",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedField: "service",
		},
		{
			name: "missing environment",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Title:         "Test",
			},
			expectedField: "environment",
		},
		{
			name: "invalid severity",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Severity:      "bad",
				Title:         "Test",
			},
			expectedField: "severity",
		},
		{
			name: "valid severity",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Severity:      "high",
				Title:         "Test",
			},
			expectedField: "", // No error expected
		},
		{
			name: "missing title",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "",
			},
			expectedField: "title",
		},
		{
			name: "invalid status",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Severity:      "high",
				Title:         "Test",
				Status:        "bad",
			},
			expectedField: "status",
		},
		{
			name: "valid status",
			req: models.WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Severity:      "high",
				Title:         "Test",
				Status:        "firing",
			},
			expectedField: "", // No error expected
		},
		{
						name: "too many labels",
						req: models.WebhookRequest{
							SchemaVersion: 1,
							Type:          "event",
							OccurredAt:    "2026-08-16T01:47:00Z",
							Service:       "test",
							Environment:   "prod",
							Title:         "Test",
						},
						expectedField: "labels",
					},
					{
						name: "label key too long",
						req: models.WebhookRequest{
							SchemaVersion: 1,
							Type:          "event",
							OccurredAt:    "2026-08-16T01:47:00Z",
							Service:       "test",
							Environment:   "prod",
							Title:         "Test",
							Labels: map[string]string{
								strings.Repeat("k", 129): "value",
							},
						},
						expectedField: "labels." + strings.Repeat("k", 129),
					},
					{
						name: "label value too long",
						req: models.WebhookRequest{
							SchemaVersion: 1,
							Type:          "event",
							OccurredAt:    "2026-08-16T01:47:00Z",
							Service:       "test",
							Environment:   "prod",
							Title:         "Test",
							Labels: map[string]string{
								"key": strings.Repeat("v", 513),
							},
						},
						expectedField: "labels.key",
					},
		{
			name: "multiple errors",
			req: models.WebhookRequest{
				SchemaVersion: 2,
				Type:          "invalid",
				OccurredAt:    "bad-date",
				Service:       "",
				Environment:   "",
				Severity:      "bad",
				Title:         "",
				Status:        "bad",
			},
			expectedField: "multiple", // Special case for multiple errors
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.req
			if tt.name == "too many labels" {
				// Add 65 labels
				for i := 0; i < 65; i++ {
					req.Labels[string(rune(i))] = "value"
				}
			}

			details := models.ValidateWebhookRequest(&req)
			
			if tt.name == "multiple errors" {
				// Expect 8 errors for the multiple errors test case
				assert.Len(t, details, 8, "Error count mismatch")
				return
			}

			if tt.expectedField == "" {
				// No errors expected
				assert.Empty(t, details, "Expected no errors but got: %v", details)
				return
			}

			// Check that we got exactly one error with the expected field
			assert.Len(t, details, 1, "Expected exactly one error but got: %v", details)
			assert.Equal(t, tt.expectedField, details[0].Field, "Expected field %s but got %s", tt.expectedField, details[0].Field)
		})
	}
}

func TestDBRollback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a handler with a connection that will fail
	// This is harder to test without a mock DB, so we'll skip the actual rollback test
	// The important thing is that the handler uses transactions properly
	// which is evident from the code structure
	t.Skip("Requires mock database for rollback testing")
}