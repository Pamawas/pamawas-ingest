package models

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidSeverity(t *testing.T) {
	assert.True(t, ValidSeverity("debug"))
	assert.True(t, ValidSeverity("info"))
	assert.True(t, ValidSeverity("warning"))
	assert.True(t, ValidSeverity("high"))
	assert.True(t, ValidSeverity("critical"))
	assert.False(t, ValidSeverity("invalid"))
	assert.False(t, ValidSeverity(""))
	assert.False(t, ValidSeverity("HIGH"))
}

func TestValidStatus(t *testing.T) {
	assert.True(t, ValidStatus("firing"))
	assert.True(t, ValidStatus("resolved"))
	assert.True(t, ValidStatus("informational"))
	assert.False(t, ValidStatus("invalid"))
	assert.False(t, ValidStatus(""))
}

func TestValidEventType(t *testing.T) {
	assert.True(t, ValidEventType("alert"))
	assert.True(t, ValidEventType("event"))
	assert.False(t, ValidEventType("invalid"))
	assert.False(t, ValidEventType(""))
}

func TestValidateWebhookRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *WebhookRequest
		expectedCount int
		expectedFields []string
	}{
		{
			name: "valid request",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedCount: 0,
		},
		{
			name: "missing schema_version",
			req: &WebhookRequest{
				Type:       "event",
				OccurredAt: "2026-08-16T01:47:00Z",
				Service:    "test",
				Environment: "prod",
				Title:      "Test",
			},
			expectedCount: 1,
			expectedFields: []string{"schema_version"},
		},
		{
			name: "invalid schema_version",
			req: &WebhookRequest{
				SchemaVersion: 2,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedCount: 1,
			expectedFields: []string{"schema_version"},
		},
		{
			name: "missing type",
			req: &WebhookRequest{
				SchemaVersion: 1,
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedCount: 1,
			expectedFields: []string{"type"},
		},
		{
			name: "invalid type",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "invalid",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedCount: 1,
			expectedFields: []string{"type"},
		},
		{
			name: "missing occurred_at",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedCount: 1,
			expectedFields: []string{"occurred_at"},
		},
		{
			name: "invalid occurred_at format",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "not-a-date",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedCount: 1,
			expectedFields: []string{"occurred_at"},
		},
		{
			name: "missing service",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Environment:   "prod",
				Title:         "Test",
			},
			expectedCount: 1,
			expectedFields: []string{"service"},
		},
		{
			name: "missing environment",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Title:         "Test",
			},
			expectedCount: 1,
			expectedFields: []string{"environment"},
		},
		{
			name: "invalid severity",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Severity:      "invalid",
				Title:         "Test",
			},
			expectedCount: 1,
			expectedFields: []string{"severity"},
		},
		{
			name: "valid severity",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Severity:      "high",
				Title:         "Test",
			},
			expectedCount: 0,
		},
		{
			name: "missing title",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
			},
			expectedCount: 1,
			expectedFields: []string{"title"},
		},
		{
			name: "invalid status",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
				Status:        "invalid",
			},
			expectedCount: 1,
			expectedFields: []string{"status"},
		},
		{
			name: "valid status",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
				Status:        "firing",
			},
			expectedCount: 0,
		},
		{
			name: "too many labels",
			req: &WebhookRequest{
				SchemaVersion: 1,
				Type:          "event",
				OccurredAt:    "2026-08-16T01:47:00Z",
				Service:       "test",
				Environment:   "prod",
				Title:         "Test",
				Labels:        make(map[string]string),
			},
			expectedCount: 1,
			expectedFields: []string{"labels"},
		},
		{
					name: "label key too long",
					req: &WebhookRequest{
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
					expectedCount:  1,
					expectedFields: []string{"labels." + strings.Repeat("k", 129)},
				},
				{
					name: "label value too long",
					req: &WebhookRequest{
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
					expectedCount:  1,
					expectedFields: []string{"labels.key"},
				},
		{
			name: "multiple errors",
			req: &WebhookRequest{
				SchemaVersion: 2,
				Type:          "invalid",
				OccurredAt:    "bad-date",
				Service:       "",
				Environment:   "",
				Severity:      "bad",
				Title:         "",
				Status:        "bad",
			},
			expectedCount: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "too many labels" {
				// Add 65 labels
				for i := 0; i < 65; i++ {
					tt.req.Labels[string(rune(i))] = "value"
				}
			}

			details := ValidateWebhookRequest(tt.req)
			assert.Equal(t, tt.expectedCount, len(details), "Error count mismatch")

			for _, field := range tt.expectedFields {
				found := false
				for _, d := range details {
					if d.Field == field {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected field %s in error details", field)
			}
		})
	}
}

func TestToCommonEvent(t *testing.T) {
	req := &WebhookRequest{
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

	event := req.ToCommonEvent("grafana", "evt_123", "fp_abc")

	assert.Equal(t, "evt_123", event.ID)
	assert.Equal(t, "grafana", event.Source)
	assert.Equal(t, "grafana-alert-7842", event.SourceEventID)
	assert.Equal(t, "fp_abc", event.Fingerprint)
	assert.Equal(t, "alert", event.Type)
	assert.Equal(t, "2026-08-16T01:47:00Z", event.Timestamp)
	assert.Equal(t, "payment-api", event.Service)
	assert.Equal(t, "production", event.Environment)
	assert.Equal(t, "high", event.Severity)
	assert.Equal(t, "Database connection pool exhausted", event.Title)
	assert.Equal(t, "firing", event.Status)
	assert.Equal(t, 1, event.SchemaVersion)
	assert.Equal(t, req.Labels, event.Labels)
	assert.NotNil(t, event.OccurredAt)
}

func TestToCommonEvent_DefaultStatus(t *testing.T) {
	req := &WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
		// Status not set
	}

	event := req.ToCommonEvent("generic", "evt_123", "")
	assert.Equal(t, "firing", event.Status, "Default status should be firing")
}

func TestGrafanaToWebhookRequest(t *testing.T) {
	grafanaReq := &GrafanaWebhookRequest{
		RuleName:  "High CPU Usage",
		Timestamp: "2026-08-16T01:47:00Z",
		Tags: map[string]string{
			"service":   "payment-api",
			"severity":  "high",
			"namespace": "payments",
			"team":      "platform",
		},
		EvalMatches: []EvalMatch{
			{
				Time: "2026-08-16T01:47:00Z",
				Metric: map[string]string{
					"instance": "server-1",
					"job":      "node-exporter",
				},
			},
		},
		RawPayload: map[string]interface{}{
			"ruleName": "High CPU Usage",
		},
	}

	req := grafanaReq.ToWebhookRequest()

	assert.Equal(t, 1, req.SchemaVersion)
	assert.Equal(t, "alert", req.Type)
	assert.Equal(t, "2026-08-16T01:47:00Z", req.OccurredAt)
	assert.Equal(t, "payment-api", req.Service)
	assert.Equal(t, "production", req.Environment) // default
	assert.Equal(t, "high", req.Severity)
	assert.Equal(t, "High CPU Usage", req.Title)
	assert.Equal(t, "firing", req.Status)
	assert.Equal(t, "payments", req.Labels["namespace"])
	assert.Equal(t, "platform", req.Labels["team"])
	assert.Equal(t, "server-1", req.Labels["instance"])
	assert.Equal(t, "node-exporter", req.Labels["job"])
	// service and severity should not be in labels
	_, hasService := req.Labels["service"]
	assert.False(t, hasService)
	_, hasSeverity := req.Labels["severity"]
	assert.False(t, hasSeverity)
}

func TestGrafanaToWebhookRequest_EvalMatchTimestamp(t *testing.T) {
	grafanaReq := &GrafanaWebhookRequest{
		RuleName: "High CPU Usage",
		EvalMatches: []EvalMatch{
			{
				Time: "2026-08-16T01:47:00Z",
			},
		},
		Tags: map[string]string{
			"service": "payment-api",
		},
	}

	req := grafanaReq.ToWebhookRequest()
	assert.Equal(t, "2026-08-16T01:47:00Z", req.OccurredAt)
}

func TestGrafanaToWebhookRequest_DefaultSeverity(t *testing.T) {
	grafanaReq := &GrafanaWebhookRequest{
		RuleName: "High CPU Usage",
		Timestamp: "2026-08-16T01:47:00Z",
		Tags: map[string]string{
			"service": "payment-api",
		},
	}

	req := grafanaReq.ToWebhookRequest()
	assert.Equal(t, "warning", req.Severity, "Default severity should be warning")
}

func TestCommonEventOccurredAtParsing(t *testing.T) {
	req := &WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00Z",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
	}

	event := req.ToCommonEvent("test", "evt_123", "")
	assert.Equal(t, time.Date(2026, 8, 16, 1, 47, 0, 0, time.UTC), event.OccurredAt)
}

func TestCommonEventOccurredAtWithOffset(t *testing.T) {
	req := &WebhookRequest{
		SchemaVersion: 1,
		Type:          "event",
		OccurredAt:    "2026-08-16T01:47:00+07:00",
		Service:       "test",
		Environment:   "prod",
		Title:         "Test",
	}

	event := req.ToCommonEvent("test", "evt_123", "")
	// 01:47 +07:00 = 18:47 UTC the previous day
	expected := time.Date(2026, 8, 15, 18, 47, 0, 0, time.UTC)
	assert.Equal(t, expected, event.OccurredAt)
}