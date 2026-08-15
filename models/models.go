package models

// CommonEvent represents the normalized event schema.
type CommonEvent struct {
	ID        string            `json:"id"`
	Source    string            `json:"source"`
	Type      string            `json:"type"`
	Timestamp string            `json:"timestamp"` // ISO8601 string
	Service   string            `json:"service,omitempty"`
	Environment string        `json:"environment,omitempty"`
	Severity  string            `json:"severity,omitempty"`
	Title     string            `json:"title,omitempty"`
	Status    string            `json:"status,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	RawPayload interface{}      `json:"raw_payload,omitempty"`
}

// WebhookResponse represents the response to webhook requests
type WebhookResponse struct {
	Status   string `json:"status"`
	EventID  string `json:"event_id,omitempty"`
	Error    string `json:"error,omitempty"`
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