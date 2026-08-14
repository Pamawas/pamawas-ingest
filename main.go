package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

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

// Executor defines the database operations we need.
type Executor interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// dbExecutor wraps *sql.DB to satisfy Executor.
type dbExecutor struct {
	*sql.DB
}

func (d *dbExecutor) Exec(query string, args ...interface{}) (sql.Result, error) {
	return d.DB.Exec(query, args...)
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

	// Set connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	r := mux.NewRouter()
	r.HandleFunc("/webhook/grafana", grafanaWebhookHandler(&dbExecutor{db})).Methods("POST")
	r.HandleFunc("/webhook/generic", genericWebhookHandler(&dbExecutor{db})).Methods("POST")
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
func grafanaWebhookHandler(db Executor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			log.Printf("Invalid JSON in grafana webhook: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Extract relevant fields from Grafana webhook with validation
		eventID := uuid.NewString()
		source := "grafana"
		eventType := "alert"

		// timestamp: prefer evalMatches[0].time, then fallback to root timestamp?
		var timestamp string
		if evalMatches, ok := payload["evalMatches"].([]interface{}); ok && len(evalMatches) > 0 {
			if firstMatch, ok := evalMatches[0].(map[string]interface{}); ok {
				if t, ok := firstMatch["time"].(string); ok {
					timestamp = t
				}
			}
		}
		if timestamp == "" {
			// fallback to root timestamp if exists
			if t, ok := payload["timestamp"].(string); ok {
				timestamp = t
			}
		}
		if timestamp == "" {
			log.Printf("Missing timestamp in grafana webhook payload")
			http.Error(w, "Missing timestamp", http.StatusBadRequest)
			return
		}

		// service from tags.service
		var service string
		if tags, ok := payload["tags"].(map[string]interface{}); ok {
			if s, ok := tags["service"].(string); ok {
				service = s
			}
		}
		if service == "" {
			log.Printf("Missing service in grafana webhook payload tags")
			http.Error(w, "Missing service", http.StatusBadRequest)
			return
		}

		// title from ruleName
		var title string
		if t, ok := payload["ruleName"].(string); ok {
			title = t
		}
		if title == "" {
			log.Printf("Missing ruleName in grafana webhook payload")
			http.Error(w, "Missing ruleName", http.StatusBadRequest)
			return
		}

		// severity: we'll use a default or try to extract from value? Keep as empty for now.
		var severity string

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
		// Copy labels from tags if present
		if tags, ok := payload["tags"].(map[string]interface{}); ok {
			for k, v := range tags {
				if str, ok := v.(string); ok {
					event.Labels[k] = str
				}
			}
		}

		// Insert into events table
		_, err := db.Exec(
			`INSERT INTO events (id, source, type, timestamp, service, environment, severity, title, status, labels, raw_payload) 
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
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
func genericWebhookHandler(db Executor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			log.Printf("Invalid JSON in generic webhook: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		eventID := uuid.NewString()
		source := "generic"
		eventType := "event"

		// timestamp from payload.timestamp
		var timestamp string
		if t, ok := payload["timestamp"].(string); ok {
			timestamp = t
		}
		if timestamp == "" {
			log.Printf("Missing timestamp in generic webhook payload")
			http.Error(w, "Missing timestamp", http.StatusBadRequest)
			return
		}

		// service from payload.service
		var service string
		if s, ok := payload["service"].(string); ok {
			service = s
		}
		if service == "" {
			log.Printf("Missing service in generic webhook payload")
			http.Error(w, "Missing service", http.StatusBadRequest)
			return
		}

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
			`INSERT INTO events (id, source, type, timestamp, service, environment, severity, title, status, labels, raw_payload) 
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
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
