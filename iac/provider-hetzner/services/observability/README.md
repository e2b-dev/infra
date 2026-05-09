# Observability Stack (NX.9) — self-hosted EU-sovereign

Sentry self-hosted (sentry.helix12.eu) + Grafana LGTM Stack (Loki + Grafana + Tempo + Mimir) + OTEL-Collector + Prometheus.

## EU-Sovereignty Replacement

| Manus → US Provider | MaxiCore → Self-Hosted EU |
|---|---|
| Sentry SaaS | Sentry self-hosted (sentry.helix12.eu) |
| Datadog/New Relic | Grafana + Mimir |
| AWS CloudWatch Logs | Loki |
| AWS X-Ray / Jaeger SaaS | Tempo |
| AWS CloudWatch Metrics | Prometheus + Mimir |

All data stays on Hetzner Object Storage (NX.2.4 buckets: `loki-chunks`, `mimir-blocks`, `tempo-traces`).

## Deploy

```bash
DEPLOY_HOST=178.105.7.48 ./deploy.sh
```

## Components (docker-compose)

- **OTEL Collector** :4317 (gRPC) + :4318 (HTTP)
- **Loki** :3100 (logs, S3-backed, 30d retention)
- **Mimir** :9009 (metrics, S3-backed, 90d retention)
- **Tempo** :3200 (traces, S3-backed, 7d retention)
- **Grafana** :3000 (UI, localhost-only via SSH-tunnel)
- **Prometheus** :9090 (24h hot tier, remote-writes to Mimir for cold)

## Sentry self-hosted

Deploy via official `sentry-self-hosted` install script (separate from this docker-compose due to its own orchestrator with Postgres, Kafka, ClickHouse, Snuba). See `scripts/install-sentry.sh` (NX.9b extension).

## Cross-Refs

- `iac/provider-hetzner/init/buckets.tf` (NX.2.4 storage extensions)
- `backend/streaming/` (NX.8a — exports OTEL traces to this stack)
- `manus-wiki/manus-4/MISSED_FORENSIK_AUDIT_MANUS4.md` (Manus uses self-hosted Sentry too)
