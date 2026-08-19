package utils

import (
	"testing"

	"github.com/Pamawas/pamawas-ingest/models"
	"github.com/stretchr/testify/assert"
)

func TestGenerateFingerprint(t *testing.T) {
	req := &models.WebhookRequest{
		SchemaVersion: 1,
		Type:          "alert",
		Service:       "payment-api",
		Environment:   "production",
		Severity:      "high",
		Title:         "Database connection pool exhausted",
		Status:        "firing",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Labels: map[string]string{
			"namespace":   "payments",
			"alert_rule":  "db_pool_exhaustion",
		},
	}

	fp1 := GenerateFingerprint(req)
	fp2 := GenerateFingerprint(req)
	assert.Equal(t, fp1, fp2, "Fingerprint should be deterministic")

	// Different request should produce different fingerprint
	req2 := *req
	req2.Title = "Different title"
	fp3 := GenerateFingerprint(&req2)
	assert.NotEqual(t, fp1, fp3, "Different content should produce different fingerprint")

	// Label order shouldn't matter
	req3 := *req
	req3.Labels = map[string]string{
		"alert_rule": "db_pool_exhaustion",
		"namespace":  "payments",
	}
	fp4 := GenerateFingerprint(&req3)
	assert.Equal(t, fp1, fp4, "Label order should not affect fingerprint")
}

func TestGenerateFingerprintFromCommon(t *testing.T) {
	event := &models.CommonEvent{
		Type:      "alert",
		Service:   "payment-api",
		Environment: "production",
		Severity:  "high",
		Title:     "Database connection pool exhausted",
		Status:    "firing",
		Timestamp: "2026-08-16T01:47:00Z",
		Labels: map[string]string{
			"namespace":   "payments",
			"alert_rule":  "db_pool_exhaustion",
		},
	}

	fp1 := GenerateFingerprintFromCommon(event)
	fp2 := GenerateFingerprintFromCommon(event)
	assert.Equal(t, fp1, fp2)
}

func TestComputeKeyHash(t *testing.T) {
	key := "test-idempotency-key"
	hash1 := ComputeKeyHash(key)
	hash2 := ComputeKeyHash(key)
	assert.Equal(t, hash1, hash2)
}

func TestComputeRequestHash(t *testing.T) {
	req := &models.WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
	}

	hash1, err := ComputeRequestHash(req)
	assert.NoError(t, err)

	hash2, err := ComputeRequestHash(req)
	assert.NoError(t, err)

	assert.Equal(t, hash1, hash2)

	// Different request should produce different hash
	req2 := *req
	req2.Title = "Different"
	hash3, err := ComputeRequestHash(&req2)
	assert.NoError(t, err)
	assert.NotEqual(t, hash1, hash3)
}

func TestGenerateIdempotencyKey(t *testing.T) {
	key1 := GenerateIdempotencyKey("grafana", "alert-123")
	assert.Equal(t, "ingest:grafana:alert-123", key1)

	key2 := GenerateIdempotencyKey("generic", "event-456")
	assert.Equal(t, "ingest:generic:event-456", key2)
}

func TestGenerateIdempotencyKeyFromFingerprint(t *testing.T) {
	key := GenerateIdempotencyKeyFromFingerprint("grafana", "abc123")
	assert.Equal(t, "ingest:grafana:fp:abc123", key)
}