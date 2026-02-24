locals {
  dashboard_api_enabled = var.dashboard_api_count > 0

  domains    = toset(concat(var.additional_domains, [var.domain_name]))
  subdomains = local.dashboard_api_enabled ? ["dashboard-api"] : []

  // Create matrix for each domain and subdomain combination
  routing_matrix = local.dashboard_api_enabled ? {
    for p in setproduct(local.domains, local.subdomains) :
    "${p[0]}|${p[1]}" => {
      domain    = p[0]
      subdomain = p[1]
    }
  } : {}
}

resource "google_compute_health_check" "ingress" {
  name = "${var.prefix}ingress"

  timeout_sec         = 3
  check_interval_sec  = 5
  healthy_threshold   = 2
  unhealthy_threshold = 2

  http_health_check {
    port         = var.ingress_port.port
    request_path = var.ingress_port.health_path
  }
}

resource "google_compute_backend_service" "ingress" {
  name = "${var.prefix}ingress"

  protocol  = "HTTP"
  port_name = var.ingress_port.name

  session_affinity = null
  health_checks    = [google_compute_health_check.ingress.id]

  timeout_sec = 65

  load_balancing_scheme = "EXTERNAL_MANAGED"
  locality_lb_policy    = "ROUND_ROBIN"

  security_policy = google_compute_security_policy.ingress.id

  backend {
    group = var.api_instance_group
  }
}

resource "google_compute_security_policy" "ingress" {
  name = "${var.prefix}ingress"

  adaptive_protection_config {
    layer_7_ddos_defense_config {
      enable = true
    }
  }
}

resource "google_compute_url_map" "ingress" {
  name            = "${var.prefix}ingress"
  default_service = google_compute_backend_service.ingress.self_link
}

resource "google_compute_global_forwarding_rule" "ingress" {
  name                  = "${var.prefix}ingress-forward-http"
  ip_protocol           = "TCP"
  port_range            = "443"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  ip_address            = google_compute_global_address.ingress_ipv4.address
  target                = google_compute_target_https_proxy.ingress.self_link
}

resource "google_compute_global_address" "ingress_ipv4" {
  name       = "${var.prefix}ingress-ipv4"
  ip_version = "IPV4"
}

resource "google_compute_ssl_policy" "ingress" {
  name            = "${var.prefix}ingress-ssl-policy"
  profile         = "MODERN"
  min_tls_version = "TLS_1_2"
}

resource "google_compute_target_https_proxy" "ingress" {
  name    = "${var.prefix}ingress-https"
  url_map = google_compute_url_map.ingress.self_link

  ssl_policy = google_compute_ssl_policy.ingress.self_link

  certificate_map = "//certificatemanager.googleapis.com/${google_certificate_manager_certificate_map.certificate_map.id}"
}

data "cloudflare_zone" "zone" {
  for_each = local.domains
  name     = each.value
}

resource "cloudflare_record" "records" {
  for_each = local.routing_matrix

  zone_id = data.cloudflare_zone.zone[each.value.domain].id
  name    = each.value.subdomain
  content = google_compute_global_forwarding_rule.ingress.ip_address
  type    = "A"
  comment = var.gcp_project_id
}
