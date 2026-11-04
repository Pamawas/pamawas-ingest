-- 003_feedback_and_reviews.sql
-- Add investigation_reviews and report_feedback tables for quality measurement
-- Per domain-data-model.md section 9 (Feedback and Quality)

CREATE TABLE IF NOT EXISTS investigation_reviews (
    id                  TEXT PRIMARY KEY,
    incident_id         TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    run_id              TEXT NOT NULL REFERENCES investigation_runs(id) ON DELETE CASCADE,
    evidence_id         TEXT REFERENCES evidence(id) ON DELETE SET NULL,
    verdict             TEXT NOT NULL CHECK (verdict IN ('correct', 'incorrect', 'partially_correct', 'unknown')),
    corrected_cause     TEXT,
    notes               TEXT,
    reviewer            TEXT NOT NULL,
    reviewed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS investigation_reviews_incident_id_idx ON investigation_reviews (incident_id);
CREATE INDEX IF NOT EXISTS investigation_reviews_run_id_idx ON investigation_reviews (run_id);
CREATE INDEX IF NOT EXISTS investigation_reviews_evidence_id_idx ON investigation_reviews (evidence_id);

CREATE TABLE IF NOT EXISTS report_feedback (
    id                  TEXT PRIMARY KEY,
    report_id           TEXT NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    rating              TEXT NOT NULL CHECK (rating IN ('useful', 'not_useful', 'wrong', 'missing_context')),
    comments            TEXT,
    reviewer            TEXT NOT NULL,
    reviewed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS report_feedback_report_id_idx ON report_feedback (report_id);