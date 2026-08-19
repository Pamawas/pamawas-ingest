# pamawas-ingest

**Webhook Ingestion API** — Normalizes alerts from Grafana, Prometheus, Loki, and generic sources into a common event schema and persists to PostgreSQL.

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?logo=docker)](https://docker.com/)

---

## Purpose

This is the **entry point** for all infrastructure events into Pamawas. It accepts webhook payloads, validates and normalizes them to the common event schema, and writes them to the PostgreSQL `events` table.

## Features

- **Generic webhook endpoint** — Accepts any JSON payload, extracts key fields
- **Grafana adapter** — Maps Grafana alert webhook format to common schema
- **PostgreSQL persistence** — Connection pooling, retries, idempotency
- **Observability built-in** — Health checks, Prometheus metrics, structured JSON logging, OpenTelemetry tracing
- **Multi-stage Docker build** — Optimized alpine images

## Quick Start

```bash
# Docker (recommended)
docker run -e DATABASE_URL="postgres://user:pass@host:5432/db" \
  -p 8080:8080 ghcr.io/yoganovvaindra/pamawas-ingest:latest

# Local development
go run main.go
```

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | **Required** |
| `PORT` | HTTP server port | `8080` |
| `LOG_LEVEL` | debug, info, warn, error | `info` |
| `ENVIRONMENT` | deployment, staging, production | `development` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Tempo OTLP gRPC endpoint | `tempo:4317` |

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/webhook/generic` | POST | Accept any JSON, normalize to common schema |
| `/webhook/grafana` | POST | Grafana alert webhook adapter |
| `/healthz` | GET | Health check with DB connectivity |
| `/ready` | GET | Readiness probe |
| `/metrics` | GET | Prometheus metrics |

## Example Webhooks

```bash
# Generic webhook
curl -X POST http://localhost:8080/webhook/generic \
  -H "Content-Type: application/json" \
  -d '{"timestamp":"2026-08-13T02:14:23Z","service":"payment-api","title":"High latency","severity":"warning"}'

# Grafana webhook
curl -X POST http://localhost:8080/webhook/grafana \
  -H "Content-Type: application/json" \
  -d '{"ruleName":"High API latency","evalMatches":[{"time":"2026-08-13T02:14:23Z"}],"tags":{"service":"payment-api"}}'
```

## Common Event Schema

```json
{
  "id": "evt_123",
  "source": "grafana",
  "type": "alert",
  "timestamp": "2026-08-13T02:14:23Z",
  "service": "payment-api",
  "environment": "production",
  "severity": "warning",
  "title": "High API latency",
  "status": "firing",
  "labels": {"cluster": "prod-a", "team": "platform"},
  "raw_payload": {}
}
```

## Observability

| Feature | Endpoint |
|---------|----------|
| Prometheus Metrics | `/metrics` — `EventsProcessedTotal`, `WebhookRequestsTotal`, `DBWriteDuration` |
| JSON Logging | stdout — trace_id, span_id, service, method, path, status_code, duration_ms |
| OpenTelemetry | OTLP gRPC → Tempo:4317 |

## Building

```bash
# Docker
docker build -t pamawas-ingest .

# Binary
go build -o pamawas-ingest main.go
```

## Related

- **Root README**: [../README.md](../README.md)
- **System Design**: [../system-design.md](../system-design.md)
- **Database Schema**: [../pamawas-schema/README.md](../pamawas-schema/README.md)
- **Docker Compose**: [../pamawas-infra/docker-compose/](../pamawas-infra/docker-compose/)