# MaxiCore APISIX Gateway (NX.6)

**1:1 Manus** — Manus production läuft auf APISIX 3.11.0 (verifiziert via Live-API-Header `api.manus.im`).

## Stack

```text
[Internet]
    ↓
[Hetzner Cloud LB :443]  (NX.2.4)
    ↓
[APISIX :9080/:9443]  ← THIS SERVICE
    ↓
[Backend services via Consul-DNS service discovery]
    ├── api.service.consul              (api/v2/*, llm-proxy, connector-proxy)
    ├── orchestrator.service.consul     (sandbox.{domain})
    ├── ws-server.service.consul        (ws-server.{domain}, NX.8a Live-Streaming)
    └── mcp-server.service.consul       (mcp.{domain})
```

## Manus 1:1 Routes (api.manus.im)

| Route | Target | Auth | Rate-Limit |
|---|---|---|---|
| `/api/v2/*` | api-pool | JWT | 200/60s |
| `/api/llm-proxy/v1/*` | api-pool (rewrite) | x-api-key | 100/60s |
| `/apiproxy.v1.ApiProxyService/CallApi` | api-pool | x-sandbox-token | 50/60s |
| `sandbox.{domain}/*` | orchestrator | JWT | 500/60s |
| `ws-server.{domain}/*` (WebSocket) | ws-server | JWT | unlimited |
| `mcp.{domain}/*` | mcp-server | x-api-key | 200/60s |

## Files

```text
apisix/
├── docker-compose.yml                — APISIX 3.11.0 + etcd + Dashboard
├── config/
│   ├── apisix.yaml                   — Core config (admin allowlist, plugins, OTEL)
│   ├── routes.yaml                   — 6 route definitions (Manus 1:1)
│   └── dashboard.yaml                — APISIX Dashboard config
├── scripts/
│   └── deploy-apisix.sh              — Deploy to Operator/any Hetzner host
└── README.md
```

## Deploy

```bash
DEPLOY_HOST=178.105.7.48 ./scripts/deploy-apisix.sh
```

5-Phase deploy: configs upload → docker-compose pull+up → health-check → UFW rules.

## Plugins Enabled (1:1 Manus production)

- **Auth**: jwt-auth, key-auth, basic-auth, openid-connect, hmac-auth
- **Rate-limit**: limit-conn, limit-count, limit-req, limit-route
- **Observability**: prometheus, opentelemetry, http-logger, kafka-logger
- **Transformation**: proxy-rewrite, response-rewrite, grpc-transcode
- **Resilience**: api-breaker (circuit breaker), traffic-split (canary)
- **Caching**: proxy-cache (50MB memory + 1GB disk)

## Security

- Admin-API (`:9180`) **localhost-only** (UFW deny external)
- Dashboard (`:9000`) **localhost-only** (SSH-tunnel for access)
- HTTPS gateway (`:9443`) cert-pinning via Hetzner Cloud LB
- JWT secret stored in Vault (`helix12/apisix/jwt-secret`)
- Real-IP via `x-forwarded-for` from Cloud LB

## EU-Sovereignty

- APISIX is Apache 2.0 (Apache Software Foundation, vendor-neutral)
- etcd is Apache 2.0 (CNCF graduated)
- All traffic stays on Hetzner network (10.0.1.0/24 + 10.10.0.0/24)
- OTEL traces → Hetzner-hosted Tempo (NX.9)

## Cross-Refs

- `manus-wiki/manus-4/MISSED_FORENSIK_AUDIT_MANUS4.md` (M56-M60: APISIX 3.11.0 verifiziert)
- `iac/provider-hetzner/modules/cloud-lb/` (NX.2.4 → forwards to APISIX)
- `iac/provider-hetzner/services/orchestrator/` (NX.5 backend service)

## License

Apache-2.0.
