package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"
	"github.com/gorilla/mux"
	"github.com/google/uuid"
)

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

func main() {
	// Get database connection string from environment
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable not set")
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	// Test connection
	if err = db.Ping(); err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}

	r := mux.NewRouter()
	r.HandleFunc("/webhook/grafana", grafanaWebhookHandler(db)).Methods("POST")
	r.HandleFunc("/webhook/generic", genericWebhookHandler(db)).Methods("POST")
	r.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}).Methods("GET")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Starting server on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

// grafanaWebhookHandler processes Grafana alert webhook payload.
func grafanaWebhookHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Extract relevant fields from Grafana webhook
		// This is a simplified example; adjust based on actual Grafana webhook format
		eventID := uuid.NewString()
		source := "grafana"
		eventType := "alert"
		timestamp := payload["evalMatches"].([]interface{})[0].(map[string]interface{})["time"].(string) // placeholder
		service := payload["tags"].(map[string]interface{})["service"].(string) // placeholder
		title := payload["ruleName"].(string)
		severity := payload["evalMatches"].([]interface{})[0].(map[string]interface{})["value"].(string) // placeholder

		event := CommonEvent{
			ID:        eventID,
			Source:    source,
			Type:      eventType,
			Timestamp: timestamp,
			Service:   service,
			Title:     title,
			Severity:  severity,
			Labels:    make(map[string]string),
			RawPayload: payload,
		}

		// Insert into events table
		_, err := db.Exec(
			"INSERT INTO events (id, source, type, timestamp, service, environment, severity, title, status, labels, raw_payload) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)",
			event.ID, event.Source, event.Type, event.Timestamp, event.Service, "", event.Severity, event.Title, "firing", event.Labels, event.RawPayload,
		)
		if err != nil {
			log.Printf("Error inserting event: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "event_id": event.ID})
	}
}

// genericWebhookHandler processes a generic JSON webhook and normalizes it.
func genericWebhookHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		eventID := uuid.NewString()
		source := "generic"
		eventType := "event"
		timestamp := payload["timestamp"].(string) // assume timestamp field exists
		service := payload["service"].(string)    // assume service field exists

		event := CommonEvent{
			ID:        eventID,
			Source:    source,
			Type:      eventType,
			Timestamp: timestamp,
			Service:   service,
			Labels:    make(map[string]string),
			RawPayload: payload,
		}

		_, err := db.Exec(
			"INSERT INTO events (id, source, type, timestamp, service, environment, severity, title, status, labels, raw_payload) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)",
			event.ID, event.Source, event.Type, event.Timestamp, event.Service, "", "", "", "firing", event.Labels, event.RawPayload,
		)
		if err != nil {
			log.Printf("Error inserting event: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "event_id": event.ID})
	}
}