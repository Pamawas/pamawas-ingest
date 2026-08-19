package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for the ingest service
type Metrics struct {
	WebhookRequestsTotal    *prometheus.CounterVec
	WebhookRequestDuration  *prometheus.HistogramVec
	DBWriteDuration         prometheus.Histogram
	EventsProcessedTotal    prometheus.Counter
	DBConnectionErrors      prometheus.Counter
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
	}
}