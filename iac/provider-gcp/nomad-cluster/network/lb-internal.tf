resource "google_dns_managed_zone" "internal" {
  name        = "${var.prefix}internal-zone"
  dns_name    = "${var.domain_name}."
  description = "Internal DNS zone for private load balancer"
  visibility  = "private"

  private_visibility_config {
    networks {
      network_url = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
    }
  }
}

resource "tls_private_key" "internal_lb" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_self_signed_cert" "internal_lb" {
  private_key_pem = tls_private_key.internal_lb.private_key_pem

  subject {
    common_name  = var.domain_name
    organization = "E2B"
  }

  validity_period_hours = 87600 # 10 years

  allowed_uses = [
    "key_encipherment",
    "digital_signature",
    "server_auth",
  ]

  dns_names = [
    var.domain_name,
    "*.${var.domain_name}",
  ]
}

resource "google_compute_region_ssl_certificate" "internal_lb" {
  name        = "${var.prefix}internal-lb-self-signed-cert"
  region      = var.gcp_region
  description = "Self-signed certificate for internal load balancer"

  certificate = tls_self_signed_cert.internal_lb.cert_pem
  private_key = tls_private_key.internal_lb.private_key_pem

  lifecycle {
    create_before_destroy = true
  }
}

# Store the CA certificate in GCP Secret Manager for nodes to use
resource "google_secret_manager_secret" "internal_lb_ca_cert" {
  secret_id = "${var.prefix}internal-lb-ca-cert"

  replication {
    auto {}
  }

  labels = var.labels
}

resource "google_secret_manager_secret_version" "internal_lb_ca_cert" {
  secret      = google_secret_manager_secret.internal_lb_ca_cert.id
  secret_data = tls_self_signed_cert.internal_lb.cert_pem
}

resource "google_compute_subnetwork" "proxy_only" {
  name          = "${var.prefix}proxy-only-subnet"
  ip_cidr_range = "10.0.0.0/23"
  region        = var.gcp_region
  network       = var.network_name
  purpose       = "REGIONAL_MANAGED_PROXY"
  role          = "ACTIVE"
}

# Firewall rule: Allow traffic from VPC to proxy subnet (for internal LB)
resource "google_compute_firewall" "internal_lb_allow_proxy" {
  name    = "${var.prefix}internal-lb-allow-proxy"
  network = var.network_name

  allow {
    protocol = "tcp"
    ports    = ["443"]
  }

  priority = 999

  direction     = "INGRESS"
  source_ranges = ["10.0.0.0/8"] # Allow from entire internal IP space
}

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
  internal_backends = {
    session              = local.backends["session"]
    api                  = local.backends["api"]
    docker-reverse-proxy = local.backends["docker-reverse-proxy"]
  }
}

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
    hosts        = ["api.${var.domain_name}"]
    path_matcher = "api-paths"
  }

  host_rule {
    hosts        = ["docker.${var.domain_name}"]
    path_matcher = "docker-reverse-proxy-paths"
  }

  host_rule {
    hosts        = ["*.${var.domain_name}"]
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

# SSL policy for internal load balancer
resource "google_compute_region_ssl_policy" "internal" {
  name            = "${var.prefix}internal"
  region          = var.gcp_region
  profile         = "MODERN"
  min_tls_version = "TLS_1_2"
}

# Internal HTTPS target proxy with TLS
resource "google_compute_region_target_https_proxy" "internal" {
  name       = "${var.prefix}internal-https-proxy"
  region     = var.gcp_region
  url_map    = google_compute_region_url_map.internal.self_link
  ssl_policy = google_compute_region_ssl_policy.internal.self_link

  ssl_certificates = [
    google_compute_region_ssl_certificate.internal_lb.self_link
  ]
}

# Internal forwarding rule
resource "google_compute_forwarding_rule" "internal" {
  name                  = "${var.prefix}internal-forwarding-rule"
  region                = var.gcp_region
  ip_protocol           = "TCP"
  port_range            = "443"
  load_balancing_scheme = "INTERNAL_MANAGED"
  target                = google_compute_region_target_https_proxy.internal.self_link
  network               = var.network_name
  labels                = var.labels

  depends_on = [
    google_compute_subnetwork.proxy_only
  ]
}

# DNS records for internal load balancer
resource "google_dns_record_set" "internal_wildcard" {
  managed_zone = google_dns_managed_zone.internal.name
  name         = "*.${var.domain_name}."
  type         = "A"
  ttl          = 300

  rrdatas = [google_compute_forwarding_rule.internal.ip_address]
}
