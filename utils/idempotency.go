package utils

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Pamawas/pamawas-ingest/models"
	"github.com/rs/zerolog/log"
)

type IdempotencyRecord struct {
	Audience        string
	Caller          string
	KeyHash         string
	RequestHash     string
	Status          string
	ResultReference string
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

const (
	IdempotencyStatusProcessing = "processing"
	IdempotencyStatusCompleted  = "completed"
	IdempotencyStatusConflict   = "conflict"
)

func ComputeKeyHash(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// ComputeRequestHash computes a hash of the canonical request.
// RawPayload is excluded because it may contain volatile field values
// (e.g., Grafana alert values) that change on replay but don't constitute
// a different logical request.
func ComputeRequestHash(req *models.WebhookRequest) (string, error) {
	hashable := struct {
		SchemaVersion int                    `json:"schema_version"`
		SourceEventID string                 `json:"source_event_id,omitempty"`
		Type          string                 `json:"type"`
		OccurredAt    string                 `json:"occurred_at"`
		Service       string                 `json:"service"`
		Environment   string                 `json:"environment"`
		Severity      string                 `json:"severity,omitempty"`
		Title         string                 `json:"title,omitempty"`
		Status        string                 `json:"status,omitempty"`
		Labels        map[string]string      `json:"labels,omitempty"`
	}{
		SchemaVersion: req.SchemaVersion,
		SourceEventID: req.SourceEventID,
		Type:          req.Type,
		OccurredAt:    req.OccurredAt,
		Service:       req.Service,
		Environment:   req.Environment,
		Severity:      req.Severity,
		Title:         req.Title,
		Status:        req.Status,
		Labels:        req.Labels,
	}
	data, err := json.Marshal(hashable)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// CheckIdempotency checks if a request is idempotent and returns the result
// Returns (eventID, duplicate, conflict, error)
func CheckIdempotency(ctx context.Context, db *sql.DB, audience, caller, idempotencyKey string, req *models.WebhookRequest) (string, bool, bool, error) {
	keyHash := ComputeKeyHash(idempotencyKey)
	requestHash, err := ComputeRequestHash(req)
	if err != nil {
		return "", false, false, err
	}

	ctx = log.With().Str("audience", audience).Str("caller", caller).Str("key_hash", keyHash[:8]).Logger().WithContext(ctx)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, false, err
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
			log.Error().Err(rbErr).Msg("Failed to rollback transaction")
		}
	}()

	var inserted bool
	err = tx.QueryRowContext(ctx, `
		INSERT INTO idempotency_records (audience, caller, key_hash, request_hash, status, result_reference, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now() + interval '24 hours')
		ON CONFLICT (audience, caller, key_hash) DO NOTHING
		RETURNING true
	`, audience, caller, keyHash, requestHash, IdempotencyStatusProcessing, "").Scan(&inserted)

	if err != nil && err != sql.ErrNoRows {
		return "", false, false, err
	}
	if !inserted {
		var existingRequestHash, existingStatus, existingResultRef string
		var existingExpiresAt time.Time
		err = tx.QueryRowContext(ctx, `
			SELECT request_hash, status, COALESCE(result_reference, ''), expires_at
			FROM idempotency_records
			WHERE audience = $1 AND caller = $2 AND key_hash = $3
			FOR UPDATE
		`, audience, caller, keyHash).Scan(&existingRequestHash, &existingStatus, &existingResultRef, &existingExpiresAt)
		if err != nil {
			return "", false, false, err
		}
		if time.Now().After(existingExpiresAt) {
			if _, err = tx.ExecContext(ctx, `
				UPDATE idempotency_records
				SET request_hash = $1, status = $2, result_reference = '', created_at = now(), updated_at = now(), expires_at = now() + interval '24 hours'
				WHERE audience = $3 AND caller = $4 AND key_hash = $5
			`, requestHash, IdempotencyStatusProcessing, audience, caller, keyHash); err != nil {
				return "", false, false, err
			}
			if err = tx.Commit(); err != nil {
				return "", false, false, err
			}
			return "", false, false, nil
		}
		if existingRequestHash != requestHash {
			return "", false, true, nil
		}
		if existingStatus == IdempotencyStatusCompleted && existingResultRef != "" {
			return existingResultRef, true, false, nil
		}
		return "", false, false, ErrStillProcessing
	}

	if err := tx.Commit(); err != nil {
		return "", false, false, err
	}
	return "", false, false, nil
}

func CompleteIdempotency(ctx context.Context, db *sql.DB, audience, caller, idempotencyKey, eventID string) error {
	keyHash := ComputeKeyHash(idempotencyKey)

	_, err := db.ExecContext(ctx, `
		UPDATE idempotency_records
		SET status = $1, result_reference = $2, updated_at = now()
		WHERE audience = $3 AND caller = $4 AND key_hash = $5
	`, IdempotencyStatusCompleted, eventID, audience, caller, keyHash)
	return err
}

// RecoverStalledIdempotency reclaims idempotency records stuck in "processing"
// status whose expires_at is in the past, resetting them to processing with a
// fresh expiration so the same logical request can be retried. Returns the
// number of records recovered.
func RecoverStalledIdempotency(ctx context.Context, db *sql.DB, audience string) (int, error) {
	result, err := db.ExecContext(ctx, `
		UPDATE idempotency_records
		SET status = $1, result_reference = '', created_at = now(), updated_at = now(), expires_at = now() + interval '24 hours'
		WHERE audience = $1 AND status = $2 AND expires_at < now()
	`, IdempotencyStatusProcessing, audience, IdempotencyStatusProcessing)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

var ErrStillProcessing = &IdempotencyError{"still processing"}

type IdempotencyError struct {
	message string
}

func (e *IdempotencyError) Error() string {
	return e.message
}

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
	return ""
}

// GenerateIdempotencyKeyFromFingerprint generates a key from fingerprint
func GenerateIdempotencyKeyFromFingerprint(source, fingerprint string) string {
	return "ingest:" + source + ":fp:" + fingerprint
}