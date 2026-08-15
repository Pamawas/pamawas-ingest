# pamawas-ingest

**Ingest API** — Generic webhook + Grafana adapter, event normalization, PostgreSQL persistence

Language: Go 1.26

## Purpose

Handles incoming webhooks from various sources (Grafana, Prometheus, Loki, generic webhooks, etc.), normalizes events to the common schema defined in the MVP design, and writes them to the PostgreSQL events table. This is the entry point for all infrastructure events into the Pamawas system.

## MVP Reference

- **MVP §10 Build Order #1**: Ingest API — one endpoint, normalize any JSON into the common event schema, write to `events`. Grafana webhook is a payload-mapping adapter on top of this.
- **MVP §5 Data Model**: Common Event Schema (events table)
- **MVP §8 Architecture Overview**: Ingest API (Go) component

## Responsibilities

- Accept webhook payloads via HTTP POST
- Validate and normalize to common event schema (MVP §5)
- Write normalized events to PostgreSQL `events` table
- Provide health check endpoint (`/healthz`) for orchestration
- Structured logging and Prometheus metrics (`/metrics`)

## Common Event Schema (MVP §5)

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
  "labels": {
    "cluster": "prod-a",
    "namespace": "payments",
    "team": "platform"
  },
  "raw_payload": {}
}
```

## Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/webhook/grafana` | POST | Grafana alert webhook adapter |
| `/webhook/generic` | POST | Generic webhook - accepts any JSON |
| `/healthz` | GET | Health check with DB connectivity |
| `/metrics` | GET | Prometheus metrics |

## Configuration (Environment Variables)

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | Required |
| `PORT` | HTTP server port | `8080` |
| `LOG_LEVEL` | Log level (debug, info, warn, error) | `info` |

## Database Schema (from pamawas-schema)

```sql
CREATE TABLE IF NOT EXISTS events (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    type TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    service TEXT,
    environment TEXT,
    severity TEXT,
    title TEXT,
    status TEXT,
    labels JSONB,
    raw_payload JSONB
);
```

## Current Implementation Status

- ✅ Generic webhook endpoint with normalization
- ✅ Grafana webhook adapter with payload mapping
- ✅ PostgreSQL persistence with connection pooling
- ✅ Health check endpoint (`/healthz`)
- ✅ Multi-stage Dockerfile (Go 1.26-alpine builder, alpine runtime)
- ✅ GitHub Actions workflow (main + dev branches, GHCR publishing)
- ⬜ Prometheus metrics endpoint (`/metrics`)
- ⬜ Structured JSON logging
- ⬜ Request/response logging middleware
- ⬜ Retry logic with exponential backoff for DB
- ⬜ Unit tests for webhook handlers (target 80%+ coverage)

## Kanban Tasks

- `t_5f87dd0d` — Design webhook schema and common event normalization (architect)
- `t_44d95588` — Implement generic webhook endpoint with normalization (backend-dev)
- `t_2c1f8b2e` — Implement Grafana webhook adapter (backend-dev)
- `t_a42376a3` — Improve database connection handling with pooling and retries (backend-dev)
- `t_ebb6cd36` — Add health check endpoint with DB connectivity check (backend-dev)
- `t_f4c05ff4` — Optimize Dockerfile with multi-stage build (devops)
- `t_f20183f2` — Write unit tests for webhook handlers (qa-dev)

## Dependencies

- **PostgreSQL** — Events table (via pamawas-schema migrations)
- **pamawas-schema** — Shared types and migrations (parent: `t_d1cdd7a9`)

## Build & Run

```bash
# Local development
go run main.go

# Docker
docker build -t pamawas-ingest .
docker run -e DATABASE_URL="postgres://..." -p 8080:8080 pamawas-ingest

# With docker-compose (when available)
docker-compose up pamawas-ingest
```

## Testing Webhooks

```bash
# Generic webhook
curl -X POST http://localhost:8080/webhook/generic \
  -H "Content-Type: application/json" \
  -d '{"timestamp":"2026-08-13T02:14:23Z","service":"payment-api","title":"High latency"}'

# Grafana webhook
curl -X POST http://localhost:8080/webhook/grafana \
  -H "Content-Type: application/json" \
  -d '{"ruleName":"High API latency","evalMatches":[{"time":"2026-08-13T02:14:23Z"}],"tags":{"service":"payment-api"}}'
```