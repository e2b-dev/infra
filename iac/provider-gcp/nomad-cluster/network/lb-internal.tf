
# ======== GOOGLE CLOUD DNS (Internal) ====================
resource "google_dns_managed_zone" "internal" {
  name        = "${var.prefix}internal-zone"
  dns_name    = "${var.internal_domain_name}."
  description = "Internal DNS zone for private load balancer"
  visibility  = "private"

  private_visibility_config {
    networks {
      network_url = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
    }
  }
}


# ======== INTERNAL LOAD BALANCER ====================

# Proxy-only subnet required for internal load balancer
resource "google_compute_subnetwork" "proxy_only" {
  name          = "${var.prefix}proxy-only-subnet"
  ip_cidr_range = "10.0.0.0/23"
  region        = var.gcp_region
  network       = var.network_name
  purpose       = "REGIONAL_MANAGED_PROXY"
  role          = "ACTIVE"
}

# Firewall rule: Allow traffic from VPC to internal load balancer
resource "google_compute_firewall" "internal_lb_allow_clients" {
  name    = "${var.prefix}internal-lb-allow-clients"
  network = var.network_name

  allow {
    protocol = "tcp"
    ports    = ["80"]
  }

  priority = 999

  direction     = "INGRESS"
  source_ranges = ["10.0.0.0/8"] # Allow from entire internal IP space
  target_tags   = [var.cluster_tag_name]
}

# Firewall rule: Allow health checks from proxy subnet to backends
resource "google_compute_firewall" "internal_lb_proxy_to_backends" {
  name    = "${var.prefix}internal-lb-proxy-to-backends"
  network = var.network_name

  allow {
    protocol = "tcp"
  }

  priority = 999

  direction     = "INGRESS"
  source_ranges = ["10.0.0.0/23"] # Proxy-only subnet
  target_tags   = [var.cluster_tag_name]
}

locals {
  # Backend services that should be available on the internal load balancer
  internal_backends = {
    session              = local.backends["session"]
    api                  = local.backends["api"]
    docker-reverse-proxy = local.backends["docker-reverse-proxy"]
  }
}

# Internal backend services (reuse health checks from external LB)
resource "google_compute_region_backend_service" "internal" {
  for_each = local.internal_backends

  name   = "${var.prefix}backend-internal-${each.key}"
  region = var.gcp_region

  port_name = lookup(each.value, "port_name", "http")
  protocol  = lookup(each.value, "protocol", "HTTP")

  timeout_sec                     = lookup(each.value, "timeout_sec")
  connection_draining_timeout_sec = 1

  load_balancing_scheme = "INTERNAL_MANAGED"
  health_checks         = [google_compute_health_check.default[each.key].self_link]

  log_config {
    enable = var.environment != "dev"
  }

  dynamic "backend" {
    for_each = toset(each.value["groups"])
    content {
      group           = lookup(backend.value, "group")
      balancing_mode  = "UTILIZATION"
      capacity_scaler = 1.0
    }
  }

  depends_on = [
    google_compute_health_check.default
  ]
}

# Internal URL map for routing
resource "google_compute_region_url_map" "internal" {
  name            = "${var.prefix}internal-map"
  region          = var.gcp_region
  default_service = google_compute_region_backend_service.internal["api"].self_link

  host_rule {
    hosts        = ["api.${var.internal_domain_name}"]
    path_matcher = "api-paths"
  }

  host_rule {
    hosts        = ["docker.${var.internal_domain_name}"]
    path_matcher = "docker-reverse-proxy-paths"
  }

  host_rule {
    hosts        = ["*.${var.internal_domain_name}"]
    path_matcher = "session-paths"
  }

  path_matcher {
    name            = "api-paths"
    default_service = google_compute_region_backend_service.internal["api"].self_link
  }

  path_matcher {
    name            = "docker-reverse-proxy-paths"
    default_service = google_compute_region_backend_service.internal["docker-reverse-proxy"].self_link
  }

  path_matcher {
    name            = "session-paths"
    default_service = google_compute_region_backend_service.internal["session"].self_link
  }
}

# Internal HTTP target proxy (no TLS)
resource "google_compute_region_target_http_proxy" "internal" {
  name    = "${var.prefix}internal-http-proxy"
  region  = var.gcp_region
  url_map = google_compute_region_url_map.internal.self_link
}

# Internal forwarding rule
resource "google_compute_forwarding_rule" "internal" {
  name                  = "${var.prefix}internal-forwarding-rule"
  region                = var.gcp_region
  ip_protocol           = "TCP"
  port_range            = "80"
  load_balancing_scheme = "INTERNAL_MANAGED"
  target                = google_compute_region_target_http_proxy.internal.self_link
  network               = var.network_name
  labels                = var.labels

  depends_on = [
    google_compute_subnetwork.proxy_only
  ]
}

# DNS records for internal load balancer
resource "google_dns_record_set" "internal_api" {
  managed_zone = google_dns_managed_zone.internal.name
  name         = "api.${var.internal_domain_name}."
  type         = "A"
  ttl          = 300

  rrdatas = [google_compute_forwarding_rule.internal.ip_address]
}

resource "google_dns_record_set" "internal_docker" {
  managed_zone = google_dns_managed_zone.internal.name
  name         = "docker.${var.internal_domain_name}."
  type         = "A"
  ttl          = 300

  rrdatas = [google_compute_forwarding_rule.internal.ip_address]
}

resource "google_dns_record_set" "internal_wildcard" {
  managed_zone = google_dns_managed_zone.internal.name
  name         = "*.${var.internal_domain_name}."
  type         = "A"
  ttl          = 300

  rrdatas = [google_compute_forwarding_rule.internal.ip_address]
}
