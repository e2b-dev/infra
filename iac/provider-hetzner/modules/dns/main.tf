/**
 * Hetzner DNS Module — 1:1 Manus DNS-Pattern
 *
 * Manages Hetzner DNS zone records that mirror Manus's domain layout:
 *   - api.{domain}              → Public-API + Connector-Proxy + LLM-Proxy
 *   - app.{domain}              → Frontend / Web-UI
 *   - sandbox.{domain}          → Sandbox-Runtime ingress
 *   - ws-server.{domain}        → WebSocket :19780 (Live-Streaming, Manus-Pattern)
 *   - mcp.{domain}              → MCP-Server :8350 (OAuth-trigger)
 *   - nomad.{domain}            → Nomad UI (cluster-internal access)
 *   - consul.{domain}           → Consul UI (cluster-internal access)
 *   - clickhouse.{domain}       → ClickHouse HTTP (analytics)
 *   - sentry.{domain}           → Self-hosted Sentry (NX.9 observability)
 *   - grafana.{domain}          → Grafana dashboards (NX.9)
 *   - vm.{domain}               → VPS-Pattern: {port}-{vps_id}-{deploy_id}.vm.{domain}
 *
 * Pattern follows Manus's manus.im / manusvm.computer scheme verified in
 * manus-wiki/MEGA_FORENSIK_REPORT.md Sec 6 (APIs/Verknüpfungen).
 *
 * Hetzner DNS API: https://dns.hetzner.com/api-docs
 * Provider: germanbrew/hetznerdns
 */

terraform {
  required_providers {
    hetznerdns = {
      source  = "germanbrew/hetznerdns"
      version = "~> 3.0"
    }
  }
}

# ─────────────────────────── Zone Lookup ───────────────────────────

data "hetznerdns_zone" "main" {
  name = var.domain_root
}

# ─────────────────────────── Apex / Root A Record ───────────────────────────
# Optional: only created if create_apex_a_record = true.
# Useful when the domain root itself should resolve to the LB.

resource "hetznerdns_record" "apex_a" {
  count = var.create_apex_a_record ? 1 : 0

  zone_id = data.hetznerdns_zone.main.id
  name    = var.domain_name == var.domain_root ? "@" : trimsuffix(var.domain_name, ".${var.domain_root}")
  type    = "A"
  value   = var.lb_ipv4
  ttl     = 3600
}

resource "hetznerdns_record" "apex_aaaa" {
  count = var.create_apex_a_record && var.lb_ipv6 != "" ? 1 : 0

  zone_id = data.hetznerdns_zone.main.id
  name    = var.domain_name == var.domain_root ? "@" : trimsuffix(var.domain_name, ".${var.domain_root}")
  type    = "AAAA"
  value   = var.lb_ipv6
  ttl     = 3600
}

# ─────────────────────────── Wildcard CNAME *.{domain} → LB ───────────────────────────
# Routes all subdomain traffic to the Hetzner Cloud Load Balancer.
# This is the 1:1 equivalent of Manus's `*.manus.im → ALB CNAME`.

resource "hetznerdns_record" "wildcard_cname" {
  count = var.lb_hostname != "" ? 1 : 0

  zone_id = data.hetznerdns_zone.main.id
  name    = var.domain_name == var.domain_root ? "*" : "*.${trimsuffix(var.domain_name, ".${var.domain_root}")}"
  type    = "CNAME"
  value   = "${var.lb_hostname}."
  ttl     = 3600
}

# Fallback to A-record if no LB hostname (direct IP routing).
resource "hetznerdns_record" "wildcard_a" {
  count = var.lb_hostname == "" && var.lb_ipv4 != "" ? 1 : 0

  zone_id = data.hetznerdns_zone.main.id
  name    = var.domain_name == var.domain_root ? "*" : "*.${trimsuffix(var.domain_name, ".${var.domain_root}")}"
  type    = "A"
  value   = var.lb_ipv4
  ttl     = 3600
}

# ─────────────────────────── Manus-Pattern Subdomains (1:1 mapping) ───────────────────────────

locals {
  manus_subdomains = {
    api        = "Public-API + Connector-Proxy + LLM-Proxy (api.manus.im)"
    app        = "Frontend / Web-UI (app.manus.im)"
    sandbox    = "Sandbox-Runtime ingress"
    ws-server  = "WebSocket :19780 (Live-Streaming, Manus-Pattern)"
    mcp        = "MCP-Server :8350 (OAuth-trigger)"
    nomad      = "Nomad UI (cluster-internal access)"
    consul     = "Consul UI (cluster-internal access)"
    clickhouse = "ClickHouse HTTP (analytics)"
  }

  # When the domain itself is a subdomain, prepend each label with the existing prefix.
  # Otherwise records are created at the root level.
  base_prefix = var.domain_name == var.domain_root ? "" : "${trimsuffix(var.domain_name, ".${var.domain_root}")}."
}

resource "hetznerdns_record" "manus_pattern_subdomains" {
  for_each = var.lb_hostname != "" ? local.manus_subdomains : {}

  zone_id = data.hetznerdns_zone.main.id
  name    = "${local.base_prefix}${each.key}"
  type    = "CNAME"
  value   = "${var.lb_hostname}."
  ttl     = 3600
}

# Fallback A-records when no LB-hostname.
resource "hetznerdns_record" "manus_pattern_subdomains_a" {
  for_each = var.lb_hostname == "" && var.lb_ipv4 != "" ? local.manus_subdomains : {}

  zone_id = data.hetznerdns_zone.main.id
  name    = "${local.base_prefix}${each.key}"
  type    = "A"
  value   = var.lb_ipv4
  ttl     = 3600
}

# ─────────────────────────── VPS-Pattern *.vm.{domain} ───────────────────────────
# 1:1 of Manus's manusvm.computer pattern:
#   {port}-{vps_id}-{deploy_id}.vm.{domain}
# All VPS subdomains are caught by *.vm.{domain} CNAME → vm-ingress on cluster.

resource "hetznerdns_record" "vps_wildcard" {
  count = var.lb_hostname != "" ? 1 : 0

  zone_id = data.hetznerdns_zone.main.id
  name    = "${local.base_prefix}*.vm"
  type    = "CNAME"
  value   = "${var.lb_hostname}."
  ttl     = 3600
}

# ─────────────────────────── Additional User-Defined Records ───────────────────────────
# Allow callers to add extra DNS records (e.g. MX, TXT for SPF/DKIM, custom CNAMEs).

resource "hetznerdns_record" "additional" {
  for_each = var.additional_records

  zone_id = data.hetznerdns_zone.main.id
  name    = each.value.name
  type    = each.value.type
  value   = each.value.value
  ttl     = each.value.ttl
}
