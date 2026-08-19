package utils

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/Pamawas/pamawas-ingest/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckIdempotency tests the idempotency checking logic
func TestCheckIdempotency(t *testing.T) {
	// This test requires a real database connection
	// Run with TEST_DATABASE_URL set to a test database
	dbURL := "postgres://pamawas:***@localhost:5432/pamawas_test?sslmode=disable"
	if testingDB := getenv("TEST_DATABASE_URL", ""); testingDB != "" {
		dbURL = testingDB
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Clean up
	_, err = db.Exec(`TRUNCATE TABLE idempotency_records RESTART IDENTITY CASCADE;`)
	require.NoError(t, err)

	req := &models.WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
	}

	// First call - should create processing record
	eventID, duplicate, conflict, err := CheckIdempotency(db, "ingest", "generic", "test-key-1", req)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, conflict)
	assert.Empty(t, eventID)

	// Second call with same key and request - should return duplicate
	eventID, duplicate, conflict, err = CheckIdempotency(db, "ingest", "generic", "test-key-1", req)
	require.NoError(t, err)
	assert.False(t, duplicate) // Still processing, not completed yet
	assert.False(t, conflict)
	assert.Empty(t, eventID)

	// Complete the idempotency record
	err = CompleteIdempotency(db, "ingest", "generic", "test-key-1", "evt_123")
	require.NoError(t, err)

	// Third call - should return the completed event ID
	eventID, duplicate, conflict, err = CheckIdempotency(db, "ingest", "generic", "test-key-1", req)
	require.NoError(t, err)
	assert.True(t, duplicate)
	assert.Equal(t, "evt_123", eventID)

	// Different request with same key - should conflict
	req2 := *req
	req2.Title = "Different"
	eventID, duplicate, conflict, err = CheckIdempotency(db, "ingest", "generic", "test-key-1", &req2)
	require.NoError(t, err)
	assert.True(t, conflict)
}

// TestCompleteIdempotency tests completing an idempotency record
func TestCompleteIdempotency(t *testing.T) {
	dbURL := "postgres://pamawas:***@localhost:5432/pamawas_test?sslmode=disable"
	if testingDB := getenv("TEST_DATABASE_URL", ""); testingDB != "" {
		dbURL = testingDB
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`TRUNCATE TABLE idempotency_records RESTART IDENTITY CASCADE;`)
	require.NoError(t, err)

	req := &models.WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
	}

	// Create processing record
	_, _, _, err = CheckIdempotency(db, "ingest", "generic", "complete-key", req)
	require.NoError(t, err)

	// Complete it
	err = CompleteIdempotency(db, "ingest", "generic", "complete-key", "evt_456")
	require.NoError(t, err)

	// Verify it's completed
	var status string
	var resultRef string
	err = db.QueryRow(
		`SELECT status, result_reference FROM idempotency_records
		WHERE audience = $1 AND caller = $2 AND key_hash = $3`,
		"ingest", "generic", ComputeKeyHash("complete-key")).Scan(&status, &resultRef)
	require.NoError(t, err)
	assert.Equal(t, "completed", status)
	assert.Equal(t, "evt_456", resultRef)
}

// TestIdempotencyExpiration tests that expired records are treated as new
func TestIdempotencyExpiration(t *testing.T) {
	dbURL := "postgres://pamawas:***@localhost:5432/pamawas_test?sslmode=disable"
	if testingDB := getenv("TEST_DATABASE_URL", ""); testingDB != "" {
		dbURL = testingDB
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`TRUNCATE TABLE idempotency_records RESTART IDENTITY CASCADE;`)
	require.NoError(t, err)

	req := &models.WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
	}

	// Insert an expired record directly
	keyHash := ComputeKeyHash("expired-key")
	requestHash, _ := ComputeRequestHash(req)
	_, err = db.Exec(
		`INSERT INTO idempotency_records (audience, caller, key_hash, request_hash, status, result_reference, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		"ingest", "generic", keyHash, requestHash, "completed", "evt_old", time.Now().Add(-2*time.Hour), time.Now().Add(-1*time.Hour))
	require.NoError(t, err)

	// CheckIdempotency should create a new record since the old one is expired
	eventID, duplicate, conflict, err := CheckIdempotency(db, "ingest", "generic", "expired-key", req)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, conflict)
	assert.Empty(t, eventID)
}

// getenv helper for tests
func getenv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}