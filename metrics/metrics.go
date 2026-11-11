package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for the ingest service
type Metrics struct {
	WebhookRequestsTotal   *prometheus.CounterVec
	WebhookRequestDuration *prometheus.HistogramVec
	DBWriteDuration        prometheus.Histogram
	EventsProcessedTotal   prometheus.Counter
	DBConnectionErrors     prometheus.Counter
	IdempotencyHitTotal    *prometheus.CounterVec
	IdempotencyConflictTotal *prometheus.CounterVec
	CircuitBreakerState    *prometheus.GaugeVec
	CircuitBreakerFailures *prometheus.CounterVec
	StalledIdempotencyTotal prometheus.Counter
}

// NewMetrics creates and registers all metrics with default registry
func NewMetrics() *Metrics {
	return newMetrics(prometheus.DefaultRegisterer)
}

// NewMetricsWithRegistry creates and registers all metrics with a custom registry
func NewMetricsWithRegistry(reg prometheus.Registerer) *Metrics {
	return newMetrics(reg)
}

func newMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	return &Metrics{
		WebhookRequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ingest_webhook_requests_total",
				Help: "Total number of webhook requests received",
			},
			[]string{"endpoint", "status"},
		),
		WebhookRequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "ingest_webhook_request_duration_seconds",
				Help:    "Webhook request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint"},
		),
		DBWriteDuration: factory.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "ingest_db_write_duration_seconds",
				Help:    "Database write duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
		),
		EventsProcessedTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "ingest_events_processed_total",
				Help: "Total number of events successfully processed",
			},
		),
		DBConnectionErrors: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "ingest_db_connection_errors_total",
				Help: "Total number of database connection errors",
			},
		),
		IdempotencyHitTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ingest_idempotency_hit_total",
				Help: "Total number of idempotent (duplicate) webhook requests",
			},
			[]string{"endpoint"},
		),
		IdempotencyConflictTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ingest_idempotency_conflict_total",
				Help: "Total number of idempotency conflicts (same key, different request)",
			},
			[]string{"endpoint"},
		),
		CircuitBreakerState: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ingest_circuit_breaker_state",
				Help: "Circuit breaker state per dependency (0=closed, 1=open, 2=half-open)",
			},
			[]string{"dependency"},
		),
		CircuitBreakerFailures: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ingest_circuit_breaker_failures_total",
				Help: "Total number of circuit breaker failures per dependency",
			},
			[]string{"dependency"},
		),
		StalledIdempotencyTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "ingest_stalled_idempotency_total",
				Help: "Total number of stalled idempotency records recovered",
			},
		),
	}
}
