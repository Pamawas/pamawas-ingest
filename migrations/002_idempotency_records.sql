-- 002_idempotency_records.sql
-- Add idempotency_records table for deduplication and replay protection

CREATE TABLE IF NOT EXISTS idempotency_records (
    audience            TEXT NOT NULL,
    caller              TEXT NOT NULL,
    key_hash            TEXT NOT NULL,
    request_hash        TEXT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('processing', 'completed', 'conflict')),
    result_reference    TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (audience, caller, key_hash)
);

CREATE INDEX IF NOT EXISTS idempotency_records_expires_at_idx ON idempotency_records (expires_at);