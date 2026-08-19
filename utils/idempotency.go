package utils

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/Pamawas/pamawas-ingest/models"
)

// IdempotencyRecord represents an idempotency record in the database
type IdempotencyRecord struct {
	Audience       string
	Caller         string
	KeyHash        string
	RequestHash    string
	Status         string
	ResultReference string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// IdempotencyStatus constants
const (
	IdempotencyStatusProcessing = "processing"
	IdempotencyStatusCompleted  = "completed"
	IdempotencyStatusConflict   = "conflict"
)

// ComputeKeyHash computes a hash of the idempotency key
func ComputeKeyHash(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// ComputeRequestHash computes a hash of the canonical request
func ComputeRequestHash(req *models.WebhookRequest) (string, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// CheckIdempotency checks if a request is idempotent and returns the result
// Returns (eventID, duplicate, conflict, error)
func CheckIdempotency(db *sql.DB, audience, caller, idempotencyKey string, req *models.WebhookRequest) (string, bool, bool, error) {
	keyHash := ComputeKeyHash(idempotencyKey)
	requestHash, err := ComputeRequestHash(req)
	if err != nil {
		return "", false, false, err
	}

	ctx := log.With().Str("audience", audience).Str("caller", caller).Str("key_hash", keyHash[:8]).Logger().WithContext(context.TODO())

	// Start a transaction for atomic check-and-insert
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, false, err
	}
	// Rollback on error, commit on success
	defer func() {
		_ = tx.Rollback()
	}()

	// Try to insert or find existing record
	var existingRequestHash string
	var existingStatus string
	var existingResultRef string

	err = tx.QueryRowContext(ctx, `
		INSERT INTO idempotency_records (audience, caller, key_hash, request_hash, status, result_reference, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now() + interval '24 hours')
		ON CONFLICT (audience, caller, key_hash) DO UPDATE SET
			request_hash = EXCLUDED.request_hash,
			result_reference = CASE 
				WHEN EXCLUDED.result_reference != '' THEN EXCLUDED.result_reference
				ELSE idempotency_records.result_reference
			END,
			expires_at = EXCLUDED.expires_at
		RETURNING request_hash, status, result_reference
	`, audience, caller, keyHash, requestHash, IdempotencyStatusProcessing, "").Scan(&existingRequestHash, &existingStatus, &existingResultRef)

	if err != nil {
		// Check if it's a unique constraint violation on different hash (conflict)
		if err == sql.ErrNoRows {
			// This shouldn't happen with ON CONFLICT, but handle it
			return "", false, false, err
		}
		// Check if we got a conflict due to different request hash
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			// Fetch the existing record
			err = tx.QueryRowContext(ctx, `
				SELECT request_hash, status, result_reference FROM idempotency_records
				WHERE audience = $1 AND caller = $2 AND key_hash = $3
			`, audience, caller, keyHash).Scan(&existingRequestHash, &existingStatus, &existingResultRef)
			if err != nil {
				return "", false, false, err
			}
			if existingRequestHash != requestHash {
				return "", false, true, nil // Conflict: same key, different request
			}
			// Same hash - return existing result if completed
			if existingStatus == IdempotencyStatusCompleted && existingResultRef != "" {
				return existingResultRef, true, false, nil
			}
			// Still processing
			return "", false, false, ErrStillProcessing
		}
		return "", false, false, err
	}

	// Record exists - check if it's completed (could be from a previous run)
	if existingStatus == IdempotencyStatusCompleted && existingResultRef != "" {
		return existingResultRef, true, false, nil
	}

	// Successfully inserted new record (processing) or updated existing processing record
	if err := tx.Commit(); err != nil {
		return "", false, false, err
	}
	return "", false, false, nil
}

// CompleteIdempotency marks an idempotency record as completed with the event ID
func CompleteIdempotency(db *sql.DB, audience, caller, idempotencyKey, eventID string) error {
	keyHash := ComputeKeyHash(idempotencyKey)
	ctx := context.TODO()

	_, err := db.ExecContext(ctx, `
		UPDATE idempotency_records
		SET status = $1, result_reference = $2, updated_at = now()
		WHERE audience = $3 AND caller = $4 AND key_hash = $5
	`, IdempotencyStatusCompleted, eventID, audience, caller, keyHash)
	return err
}

// ErrStillProcessing indicates the idempotency record is still being processed
var ErrStillProcessing = &IdempotencyError{"still processing"}

// IdempotencyError represents an idempotency-specific error
type IdempotencyError struct {
	message string
}

func (e *IdempotencyError) Error() string {
	return e.message
}

// IsConflict checks if the error is a conflict error
func IsConflict(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*IdempotencyError)
	return ok
}

// GenerateIdempotencyKey generates a deterministic idempotency key from request fields
// For webhooks, this is typically provided by the client via X-Idempotency-Key header
// For internal calls, we generate from business operation key
func GenerateIdempotencyKey(source, sourceEventID string) string {
	if sourceEventID != "" {
		return "ingest:" + source + ":" + sourceEventID
	}
	// For fingerprint-based deduplication, we'll use a different approach
	return ""
}

// GenerateIdempotencyKeyFromFingerprint generates a key from fingerprint
func GenerateIdempotencyKeyFromFingerprint(source, fingerprint string) string {
	return "ingest:" + source + ":fp:" + fingerprint
}