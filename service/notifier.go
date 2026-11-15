package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// AlertClient forwards alert lifecycle messages to the reporter, which owns
// the delivery channels and their credentials:
//
//   - /api/v1/alerts          — immediate high-severity alert when an event
//     fires (event-driven, so the user is notified as soon as the webhook is
//     persisted rather than on the correlator's or scheduler's next cycle);
//   - /api/v1/alerts/resolved — alert-resolved message when it recovers.
//
// The payload is event-sourced: at alert time the incident does not exist yet,
// so the alert carries the event's own details and is deduplicated by key.
type AlertClient struct {
	httpClient *http.Client
	baseURL    string
	wg         sync.WaitGroup
	ctx        context.Context
}

// NewAlertClient creates an AlertClient posting to baseURL.
func NewAlertClient(baseURL string, timeout time.Duration, ctx context.Context) *AlertClient {
	return &AlertClient{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    strings.TrimRight(baseURL, "/"),
		ctx:        ctx,
	}
}

// AlertPayload mirrors the reporter's alert payload.
//
// Ingest fires alerts from the event it just persisted, so it carries the
// event's own details rather than an incident id: the correlator has not
// created the incident yet, and an immediate alert must not wait for it.
type AlertPayload struct {
	RequestID       string `json:"request_id"`
	ContractVersion int    `json:"contract_version"`

	Title       string `json:"title"`
	Severity    string `json:"severity"`
	Environment string `json:"environment,omitempty"`
	Services    string `json:"services,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	DedupKey    string `json:"dedup_key"`
}

// AlertResponse mirrors the reporter's alert response.
type AlertResponse struct {
	Sent    bool   `json:"sent"`
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message"`
}

// SendAlert asks the reporter to send the immediate high-severity alert.
func (n *AlertClient) SendAlert(ctx context.Context, payload AlertPayload) (*AlertResponse, error) {
	return n.post(ctx, "/api/v1/alerts", payload)
}

// SendResolveAlert asks the reporter to send the alert-resolved message.
func (n *AlertClient) SendResolveAlert(ctx context.Context, payload AlertPayload) (*AlertResponse, error) {
	return n.post(ctx, "/api/v1/alerts/resolved", payload)
}

func (n *AlertClient) post(ctx context.Context, path string, payload AlertPayload) (*AlertResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Error().Err(closeErr).Msg("Failed to close response body")
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("reporter returned status %d for %s", resp.StatusCode, path)
	}

	var out AlertResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &out, nil
}

// SendAlertAsync dispatches the alert without blocking the webhook response.
func (n *AlertClient) SendAlertAsync(payload AlertPayload) {
	n.sendAsync("alert", n.SendAlert, payload)
}

// SendResolveAlertAsync dispatches the resolved alert without blocking.
func (n *AlertClient) SendResolveAlertAsync(payload AlertPayload) {
	n.sendAsync("resolved alert", n.SendResolveAlert, payload)
}

func (n *AlertClient) sendAsync(kind string, send func(context.Context, AlertPayload) (*AlertResponse, error), payload AlertPayload) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).
					Str("dedup_key", payload.DedupKey).
					Str("kind", kind).
					Msg("Recovered panic sending alert to reporter")
			}
		}()
		if _, err := send(n.ctx, payload); err != nil {
			log.Error().Err(err).
				Str("dedup_key", payload.DedupKey).
				Str("kind", kind).
				Msg("Failed to send alert to reporter")
		}
	}()
}

// Close waits for in-flight async sends to complete.
func (n *AlertClient) Close() {
	n.wg.Wait()
}
