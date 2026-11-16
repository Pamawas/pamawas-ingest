package handlers

import "testing"

func TestParseGrafanaPayloadStatus(t *testing.T) {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus string
	}{
		{
			name: "top-level status",
			payload: map[string]interface{}{
				"status": "resolved",
				"state":  "ok",
			},
			wantStatus: "resolved",
		},
		{
			name: "legacy state",
			payload: map[string]interface{}{
				"state": "ok",
			},
			wantStatus: "resolved",
		},
		{
			name: "alert status",
			payload: map[string]interface{}{
				"alerts": []interface{}{
					map[string]interface{}{
						"status": "resolved",
					},
				},
			},
			wantStatus: "resolved",
		},
		{
			name: "alert status with custom fields",
			payload: map[string]interface{}{
				"ruleName":  "High CPU Usage",
				"timestamp": "2026-08-16T01:52:00Z",
				"tags":      map[string]interface{}{},
				"alerts": []interface{}{
					map[string]interface{}{
						"status": "resolved",
					},
				},
			},
			wantStatus: "resolved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGrafanaPayload(tt.payload).ToWebhookRequest().Status
			if got != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

func TestParseGrafanaPayloadResolutionTimeAndFingerprint(t *testing.T) {
	payload := map[string]interface{}{
		"status": "resolved",
		"alerts": []interface{}{
			map[string]interface{}{
				"status":      "resolved",
				"startsAt":    "2026-08-16T01:47:00Z",
				"endsAt":      "2026-08-16T01:52:00Z",
				"fingerprint": "stable-alert-fingerprint",
			},
		},
	}

	req := parseGrafanaPayload(payload).ToWebhookRequest()
	if req.OccurredAt != "2026-08-16T01:52:00Z" {
		t.Fatalf("occurred at = %q, want resolved time", req.OccurredAt)
	}
	if req.Labels["grafana_fingerprint"] != "stable-alert-fingerprint" {
		t.Fatalf("Grafana fingerprint label = %q, want stable alert fingerprint", req.Labels["grafana_fingerprint"])
	}
}

// TestUnknownThresholdRanksBelowEverySeverity pins the gate's floor.
//
// HIGH_SEVERITY_THRESHOLD=unknown means "no gate": it must rank below debug so
// that an event with no severity at all (rank 0) still passes the comparison
// `severityRank(event) >= severityRank(threshold)`.
func TestUnknownThresholdRanksBelowEverySeverity(t *testing.T) {
	if got := severityRank("unknown"); got != 0 {
		t.Fatalf("severityRank(unknown) = %d, want 0", got)
	}
	for _, severity := range []string{"", "debug", "info", "warning", "high", "critical"} {
		if severityRank(severity) < severityRank("unknown") {
			t.Errorf("severity %q would be filtered by the unknown threshold", severity)
		}
	}
}
