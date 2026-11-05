package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/Pamawas/pamawas-ingest/config"
)

func TestHealthRoutes(t *testing.T) {
	router := newRouter(nil, config.Config{})

	for _, path := range []string{
		"/healthz",
		"/ready",
		"/metrics",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			match := &mux.RouteMatch{}
			if !router.Match(request, match) {
				t.Fatalf("GET %s did not match a route", path)
			}
		})
	}

	for _, path := range []string{
		"/api/v1/healthz",
		"/api/v1/ready",
		"/api/v1/metrics",
	} {
		t.Run(path+"_not_registered", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			match := &mux.RouteMatch{}
			if router.Match(request, match) {
				t.Fatalf("GET %s unexpectedly matched a route", path)
			}
		})
	}
}

// TestGrafanaWebhookHandler is a placeholder test for the grafana webhook handler.
func TestGrafanaWebhookHandler(t *testing.T) {
	t.Skip("Test not implemented yet")
}

// TestGenericWebhookHandler is a placeholder test for the generic webhook handler.
func TestGenericWebhookHandler(t *testing.T) {
	t.Skip("Test not implemented yet")
}
