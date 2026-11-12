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

// ResolveNotifier sends resolved-incident notifications to the reporter's /api/v1/resolve-notification endpoint.
type ResolveNotifier struct {
	httpClient *http.Client
	baseURL    string
	wg         sync.WaitGroup
	ctx        context.Context
}

// NewResolveNotifier creates a ResolveNotifier posting to baseURL + /api/v1/resolve-notification.
func NewResolveNotifier(baseURL string, timeout time.Duration, ctx context.Context) *ResolveNotifier {
	baseURL = strings.TrimRight(baseURL, "/")
	return &ResolveNotifier{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		ctx:        ctx,
	}
}

// ResolveNotificationPayload mirrors the reporter's payload for ingest.
type ResolveNotificationPayload struct {
	IncidentID       string `json:"incident_id"`
	RequestID        string `json:"request_id"`
	ContractVersion  int    `json:"contract_version"`
}

// ResolveNotificationResponse mirrors the reporter's response.
type ResolveNotificationResponse struct {
	IncidentID string `json:"incident_id"`
	Sent       bool   `json:"sent"`
	Skipped    bool   `json:"skipped"`
	Reason     string `json:"reason,omitempty"`
	Message    string `json:"message"`
}

// SendResolveNotification posts a resolve-notification request to the reporter and returns the decoded response.
func (n *ResolveNotifier) SendResolveNotification(ctx context.Context, payload ResolveNotificationPayload) (*ResolveNotificationResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL+"/api/v1/resolve-notification", bytes.NewReader(body))
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
		return nil, fmt.Errorf("reporter returned status %d", resp.StatusCode)
	}

	var out ResolveNotificationResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &out, nil
}

// SendResolveNotificationAsync dispatches the notification without blocking.
func (n *ResolveNotifier) SendResolveNotificationAsync(payload ResolveNotificationPayload) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Str("incident_id", payload.IncidentID).Msg("Recovered panic sending resolve notification to reporter")
			}
		}()
		if _, err := n.SendResolveNotification(n.ctx, payload); err != nil {
			log.Error().Err(err).Str("incident_id", payload.IncidentID).Msg("Failed to send resolve notification to reporter")
		}
	}()
}

// Close waits for in-flight async sends to complete.
func (n *ResolveNotifier) Close() {
	n.wg.Wait()
}
