package models

import (
	"encoding/json"
	"strings"
	"time"
)

// CommonEvent represents the normalized event schema.
type CommonEvent struct {
	ID            string            `json:"id"`
	Source        string            `json:"source"`
	SourceEventID string            `json:"source_event_id,omitempty"`
	Fingerprint   string            `json:"fingerprint,omitempty"`
	Type          string            `json:"type"`
	Timestamp     string            `json:"timestamp"` // ISO8601 string
	OccurredAt    time.Time         `json:"-"`
	Service       string            `json:"service,omitempty"`
	Environment   string            `json:"environment,omitempty"`
	Severity      string            `json:"severity,omitempty"`
	Title         string            `json:"title,omitempty"`
	Status        string            `json:"status,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	RawPayload    interface{}       `json:"raw_payload,omitempty"`
	SchemaVersion int               `json:"schema_version,omitempty"`
}

// WebhookRequest represents the v1 generic webhook request
type WebhookRequest struct {
	SchemaVersion int                    `json:"schema_version"`
	SourceEventID string                 `json:"source_event_id,omitempty"`
	Type          string                 `json:"type"`
	OccurredAt    string                 `json:"occurred_at"`
	Service       string                 `json:"service"`
	// The concrete entity the alert is about (Grafana: the `instance` label,
	// falling back to `job`). This is what tells a human what is down; a
	// Grafana alert's default payload names no service at all.
	Target string `json:"target,omitempty"`
	// The scrape job that produced the alert (Grafana: the `job` label).
	Job string `json:"job,omitempty"`

	Environment   string                 `json:"environment"`
	Severity      string                 `json:"severity,omitempty"`
	Title         string                 `json:"title,omitempty"`
	Status        string                 `json:"status,omitempty"`
	Labels        map[string]string      `json:"labels,omitempty"`
	RawPayload    map[string]interface{} `json:"raw_payload,omitempty"`
}

// WebhookResponse represents the response to webhook requests
type WebhookResponse struct {
	RequestID string `json:"request_id"`
	Data      struct {
		EventID   string `json:"event_id"`
		Status    string `json:"status"`
		Duplicate bool   `json:"duplicate"`
	} `json:"data"`
	Error *ErrorResponse `json:"error,omitempty"`
}

// ErrorResponse represents the error envelope
type ErrorResponse struct {
	Code    string        `json:"code"`
	Message string        `json:"message"`
	Details []ErrorDetail `json:"details,omitempty"`
}

// ErrorDetail represents a single error detail
type ErrorDetail struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// ValidationError represents a validation error with details
type ValidationError struct {
	Details []ErrorDetail
}

func (e *ValidationError) Error() string {
	return "webhook request validation failed"
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ReadyResponse represents the readiness check response
type ReadyResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ValidSeverity checks if the severity is valid
func ValidSeverity(s string) bool {
	switch s {
	case "debug", "info", "warning", "high", "critical":
		return true
	default:
		return false
	}
}

// ValidStatus checks if the event status is valid
func ValidStatus(s string) bool {
	switch s {
	case "firing", "resolved", "informational":
		return true
	default:
		return false
	}
}

// ValidEventType checks if the event type is valid
func ValidEventType(s string) bool {
	return s == "alert" || s == "event"
}

// ValidateWebhookRequest validates a webhook request
func ValidateWebhookRequest(req *WebhookRequest) []ErrorDetail {
	var details []ErrorDetail

	if req.SchemaVersion != 1 {
		details = append(details, ErrorDetail{Field: "schema_version", Reason: "must be 1"})
	}

	if req.Type == "" {
		details = append(details, ErrorDetail{Field: "type", Reason: "required"})
	} else if !ValidEventType(req.Type) {
		details = append(details, ErrorDetail{Field: "type", Reason: "must be 'alert' or 'event'"})
	}

	if req.OccurredAt == "" {
		details = append(details, ErrorDetail{Field: "occurred_at", Reason: "required"})
	} else if _, err := time.Parse(time.RFC3339, req.OccurredAt); err != nil {
		details = append(details, ErrorDetail{Field: "occurred_at", Reason: "must be RFC3339"})
	}

	// Service is optional at this layer: Grafana's default payload names no
	// service, only a target and job. The generic webhook enforces it at its
	// own boundary, where the caller controls the payload shape.
	if req.Environment == "" {
		details = append(details, ErrorDetail{Field: "environment", Reason: "required"})
	}

	if req.Severity != "" && !ValidSeverity(req.Severity) {
		details = append(details, ErrorDetail{Field: "severity", Reason: "must be one of: debug, info, warning, high, critical"})
	}

	if req.Title == "" {
		details = append(details, ErrorDetail{Field: "title", Reason: "required"})
	}

	if req.Status != "" && !ValidStatus(req.Status) {
		details = append(details, ErrorDetail{Field: "status", Reason: "must be one of: firing, resolved, informational"})
	}

	// Handle nil Labels map
	labels := req.Labels
	if labels == nil {
		labels = make(map[string]string)
	}

	if len(labels) > 64 {
		details = append(details, ErrorDetail{Field: "labels", Reason: "at most 64 entries"})
	}
	for k, v := range labels {
		if len(k) > 128 {
			details = append(details, ErrorDetail{Field: "labels." + k, Reason: "key at most 128 characters"})
		}
		if len(v) > 512 {
			details = append(details, ErrorDetail{Field: "labels." + k, Reason: "value at most 512 characters"})
		}
	}

	return details
}

// ToCommonEvent converts a WebhookRequest to a CommonEvent
func (r *WebhookRequest) ToCommonEvent(source string, eventID string, fingerprint string) CommonEvent {
	occurredAt, err := time.Parse(time.RFC3339, r.OccurredAt)
	if err != nil {
		// Should not happen as validation passes, but handle gracefully
		occurredAt = time.Now().UTC()
	}
	// Normalize to UTC
	occurredAt = occurredAt.UTC()
	status := r.Status
	if status == "" {
		status = "firing"
	}
	return CommonEvent{
		ID:            eventID,
		Source:        source,
		SourceEventID: r.SourceEventID,
		Fingerprint:   fingerprint,
		Type:          r.Type,
		Timestamp:     r.OccurredAt,
		OccurredAt:    occurredAt,
		Service:       r.Service,
		Environment:   r.Environment,
		Severity:      r.Severity,
		Title:         r.Title,
		Status:        status,
		Labels:        r.Labels,
		RawPayload:    r.RawPayload,
		SchemaVersion: r.SchemaVersion,
	}
}

// GrafanaWebhookRequest represents the v1 grafana webhook request (adapted)
type GrafanaWebhookRequest struct {
	RuleName    string                 `json:"ruleName"`
	Timestamp   string                 `json:"timestamp,omitempty"`
	Status      string                 `json:"status,omitempty"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
	EvalMatches []EvalMatch            `json:"evalMatches,omitempty"`
	Tags        map[string]string      `json:"tags,omitempty"`
	RawPayload  map[string]interface{} `json:"-"`
}

// EvalMatch represents a grafana eval match
type EvalMatch struct {
	Time   string            `json:"time"`
	Value  string            `json:"value"`
	Metric map[string]string `json:"metric,omitempty"`
}

// ToWebhookRequest converts a Grafana webhook to a normalized WebhookRequest
func (g *GrafanaWebhookRequest) ToWebhookRequest() *WebhookRequest {
	var occurredAt string
	if g.Timestamp != "" {
		occurredAt = g.Timestamp
	} else if len(g.EvalMatches) > 0 && g.EvalMatches[0].Time != "" {
		occurredAt = g.EvalMatches[0].Time
	}

	// The monitored entity. Grafana's default alert payload has no `service`
	// label at all — its labels name the target (`instance`) and the scrape
	// job (`job`). Deriving the service from those is truthful; defaulting to
	// the literal "grafana" was not, because that names the sender and made
	// every alert read "Services: grafana", telling the reader nothing about
	// what was actually down.
	var service, target, job string
	if g.Tags != nil {
		service = firstNonEmpty(g.Tags["service"], g.Tags["service_name"])
		target = firstNonEmpty(g.Tags["instance"], g.Tags["job"])
		job = g.Tags["job"]
	}
	// Correlation and enrichment key on the affected entity: the service when
	// the alert names one, otherwise the target it is about.
	if service == "" {
		service = target
	}

	severity := "warning"
	if g.Tags != nil {
		if s, ok := g.Tags["severity"]; ok {
			severity = s
		}
	}

	labels := make(map[string]string)
	if g.Tags != nil {
		for k, v := range g.Tags {
			if k != "service" && k != "severity" {
				labels[k] = v
			}
		}
	}
	if len(g.EvalMatches) > 0 && g.EvalMatches[0].Metric != nil {
		for k, v := range g.EvalMatches[0].Metric {
			if _, exists := labels[k]; !exists {
				labels[k] = v
			}
		}
	}
	if g.Fingerprint != "" {
		labels["grafana_fingerprint"] = g.Fingerprint
	}

	status := "firing"
	switch strings.ToLower(strings.TrimSpace(g.Status)) {
	case "alerting", "firing":
		status = "firing"
	case "ok", "resolved":
		status = "resolved"
	case "":
		// Older/custom Grafana payloads may omit status.
	default:
		status = g.Status
	}

	return &WebhookRequest{
		SchemaVersion: 1,
		Type:          "alert",
		OccurredAt:    occurredAt,
		Service:       service,
		Target:        target,
		Job:           job,
		Environment:   "production", // default, will be overridden by source config
		Severity:      severity,
		Title:         g.RuleName,
		Status:        status,
		Labels:        labels,
		RawPayload:    g.RawPayload,
	}
}

// firstNonEmpty returns the first non-empty value, or "" when none is set.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// MarshalJSON implements custom JSON marshaling for WebhookRequest
func (r WebhookRequest) MarshalJSON() ([]byte, error) {
	type Alias WebhookRequest
	return json.Marshal(&struct {
		Alias
		Labels map[string]string `json:"labels,omitempty"`
	}{
		Alias:  Alias(r),
		Labels: r.Labels,
	})
}
