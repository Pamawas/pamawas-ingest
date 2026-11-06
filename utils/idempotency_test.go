package utils

import (
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/Pamawas/pamawas-ingest/models"
	"github.com/Pamawas/pamawas-ingest/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckIdempotency tests the idempotency checking logic
func TestCheckIdempotency(t *testing.T) {
	dbURL := "postgres://pamawas:pamawas@localhost:5432/pamawas_test?sslmode=disable"
	if testingDB := getenv("TEST_DATABASE_URL", ""); testingDB != "" {
		dbURL = testingDB
	}

	db := testutils.SetupTestDB(t, dbURL, "../migrations")
	defer db.Close()

	req := &models.WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
	}

	caller := "utils-check"
	key := "test-key-check"

	// First call - should create processing record
	eventID, duplicate, conflict, err := CheckIdempotency(db, "ingest", caller, key, req)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, conflict)
	assert.Empty(t, eventID)

	// Second call with same key and request - should return ErrStillProcessing
	eventID, duplicate, conflict, err = CheckIdempotency(db, "ingest", caller, key, req)
	assert.ErrorIs(t, err, ErrStillProcessing)
	assert.False(t, duplicate)
	assert.False(t, conflict)
	assert.Empty(t, eventID)

	// Complete the idempotency record
	err = CompleteIdempotency(db, "ingest", caller, key, "evt_123")
	require.NoError(t, err)

	// Third call - should return the completed event ID
	eventID, duplicate, conflict, err = CheckIdempotency(db, "ingest", caller, key, req)
	require.NoError(t, err)
	assert.True(t, duplicate)
	assert.Equal(t, "evt_123", eventID)

	// Different request with same key - should conflict
	req2 := *req
	req2.Title = "Different"
	eventID, duplicate, conflict, err = CheckIdempotency(db, "ingest", caller, key, &req2)
	require.NoError(t, err)
	assert.True(t, conflict)
}

// TestCompleteIdempotency tests completing an idempotency record
func TestCompleteIdempotency(t *testing.T) {
	dbURL := "postgres://pamawas:pamawas@localhost:5432/pamawas_test?sslmode=disable"
	if testingDB := getenv("TEST_DATABASE_URL", ""); testingDB != "" {
		dbURL = testingDB
	}

	db := testutils.SetupTestDB(t, dbURL, "../migrations")
	defer db.Close()

	req := &models.WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
	}

	caller := "utils-complete"
	key := "test-key-complete"

	// Create processing record
	_, _, _, err := CheckIdempotency(db, "ingest", caller, key, req)
	require.NoError(t, err)

	// Complete it
	err = CompleteIdempotency(db, "ingest", caller, key, "evt_456")
	require.NoError(t, err)

	// Verify it's completed
	var status string
	var resultRef string
	err = db.QueryRow(
		`SELECT status, result_reference FROM idempotency_records
		WHERE audience = $1 AND caller = $2 AND key_hash = $3`,
		"ingest", caller, ComputeKeyHash(key)).Scan(&status, &resultRef)
	require.NoError(t, err)
	assert.Equal(t, "completed", status)
	assert.Equal(t, "evt_456", resultRef)
}

// TestIdempotencyExpiration tests that expired records are treated as new
func TestIdempotencyExpiration(t *testing.T) {
	dbURL := "postgres://pamawas:pamawas@localhost:5432/pamawas_test?sslmode=disable"
	if testingDB := getenv("TEST_DATABASE_URL", ""); testingDB != "" {
		dbURL = testingDB
	}

	db := testutils.SetupTestDB(t, dbURL, "../migrations")
	defer db.Close()

	caller := "utils-expire"
	key := "expired-key"

	req := &models.WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
	}

	// Insert an expired record directly
	keyHash := ComputeKeyHash(key)
	requestHash, err := ComputeRequestHash(req)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO idempotency_records (audience, caller, key_hash, request_hash, status, result_reference, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, now() - interval '1 hour', now() - interval '10 minutes')
	`, "ingest", caller, keyHash, requestHash, IdempotencyStatusProcessing, "")
	require.NoError(t, err)

	// Should allow new request because the existing record is expired
	eventID, duplicate, conflict, err := CheckIdempotency(db, "ingest", caller, key, req)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, conflict)
	assert.Empty(t, eventID)

	// Verify the record was updated with new expiration
	var expiresAt time.Time
	err = db.QueryRow(
		`SELECT expires_at FROM idempotency_records
		WHERE audience = $1 AND caller = $2 AND key_hash = $3`,
		"ingest", caller, keyHash).Scan(&expiresAt)
	require.NoError(t, err)
	assert.True(t, expiresAt.After(time.Now()))
}

func getenv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
