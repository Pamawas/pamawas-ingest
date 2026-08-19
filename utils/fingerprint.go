package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/Pamawas/pamawas-ingest/models"
)

// GenerateFingerprint creates a deterministic fingerprint for deduplication
// when source_event_id is not available. Uses normalized fields only.
func GenerateFingerprint(event *models.WebhookRequest) string {
	var parts []string

	parts = append(parts, "v1") // schema version prefix
	parts = append(parts, event.Type)
	parts = append(parts, event.Service)
	parts = append(parts, event.Environment)
	parts = append(parts, event.Severity)
	parts = append(parts, event.Title)
	parts = append(parts, event.Status)
	parts = append(parts, event.OccurredAt)

	// Sort labels for deterministic ordering
	if len(event.Labels) > 0 {
		keys := make([]string, 0, len(event.Labels))
		for k := range event.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, k+"="+event.Labels[k])
		}
	}

	// Create SHA256 hash
	data := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// GenerateFingerprintFromCommon creates a fingerprint from a CommonEvent
func GenerateFingerprintFromCommon(event *models.CommonEvent) string {
	var parts []string

	parts = append(parts, "v1")
	parts = append(parts, event.Type)
	parts = append(parts, event.Service)
	parts = append(parts, event.Environment)
	parts = append(parts, event.Severity)
	parts = append(parts, event.Title)
	parts = append(parts, event.Status)
	parts = append(parts, event.Timestamp)

	if len(event.Labels) > 0 {
		keys := make([]string, 0, len(event.Labels))
		for k := range event.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, k+"="+event.Labels[k])
		}
	}

	data := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}