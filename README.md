# pamawas-ingest

Ingest API — generic webhook + Grafana adapter, event normalization

Language: Go

## Purpose
Handles incoming webhooks from various sources (Grafana, generic webhooks, etc.), normalizes events to the common schema, and writes them to the PostgreSQL events table.

## Responsibilities
- Accept webhook payloads
- Validate and normalize to common event schema
- Deduplicate at ingress if needed
- Write normalized events to PostgreSQL
- Provide health/metrics endpoints

## TODO
- Implement Go HTTP server
- Define common event schema (import from pamawas-schema)
- Add Grafana webhook adapter
- Add generic webhook adapter
- Connect to PostgreSQL
- Add logging and metrics