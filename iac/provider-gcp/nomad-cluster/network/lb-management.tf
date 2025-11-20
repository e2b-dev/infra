resource "tls_private_key" "regional_management_lb" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_self_signed_cert" "regional_management_lb" {
  private_key_pem = tls_private_key.regional_management_lb.private_key_pem

  subject {
    common_name  = "E2B Regional Cert"
    organization = "E2B"
  }

  validity_period_hours = 87600 # 10 years

  allowed_uses = [
    "key_encipherment",
    "digital_signature",
    "server_auth",
  ]

  ip_addresses = [
    google_compute_address.regional_management_lb.address
  ]
}

resource "google_compute_address" "regional_management_lb" {
  name         = "${var.prefix}management-lb-ip"
  region       = var.gcp_region
  address_type = "EXTERNAL"
}

# Store the self-signed certificate as a regional SSL certificate
resource "google_compute_region_ssl_certificate" "regional_management_lb" {
  name        = "${var.prefix}management-lb-self-signed-cert"
  region      = var.gcp_region
  description = "Self-signed certificate for management external load balancer"

  certificate = tls_self_signed_cert.regional_management_lb.cert_pem
  private_key = tls_private_key.regional_management_lb.private_key_pem

  lifecycle {
    create_before_destroy = true
  }
}

# Management health check for Nomad backend
resource "google_compute_region_health_check" "regional_management_nomad" {
  name   = "${var.prefix}nomad"
  region = var.gcp_region

  timeout_sec         = 5
  check_interval_sec  = 5
  healthy_threshold   = 2
  unhealthy_threshold = 2

  http_health_check {
    port         = var.nomad_port
    request_path = "/v1/status/peers"
  }

  log_config {
    enable = false
  }
}

resource "google_compute_region_backend_service" "regional_management_nomad" {
  name   = "${var.prefix}nomad"
  region = var.gcp_region

  protocol  = "HTTP"
  port_name = "nomad"

  timeout_sec                     = 10
  connection_draining_timeout_sec = 1

  load_balancing_scheme = "EXTERNAL_MANAGED"
  health_checks         = [google_compute_region_health_check.regional_management_nomad.self_link]

  log_config {
    enable = var.environment != "dev"
  }

  backend {
    group           = var.server_instance_group
    balancing_mode  = "UTILIZATION"
    capacity_scaler = 1.0
  }

  depends_on = [
    google_compute_region_health_check.regional_management_nomad
  ]
}

resource "google_compute_region_url_map" "management" {
  name            = "${var.prefix}management-map"
  region          = var.gcp_region
  default_service = google_compute_region_backend_service.regional_management_nomad.self_link
}

resource "google_compute_region_ssl_policy" "management" {
  name            = "${var.prefix}management"
  region          = var.gcp_region
  profile         = "MODERN"
  min_tls_version = "TLS_1_2"
}

resource "google_compute_region_target_https_proxy" "management" {
  name       = "${var.prefix}management-https-proxy"
  region     = var.gcp_region
  url_map    = google_compute_region_url_map.management.self_link
  ssl_policy = google_compute_region_ssl_policy.management.self_link

  ssl_certificates = [
    google_compute_region_ssl_certificate.regional_management_lb.self_link
  ]
}

resource "google_compute_forwarding_rule" "management" {
  name                  = "${var.prefix}management-forwarding-rule"
  region                = var.gcp_region
  ip_protocol           = "TCP"
  port_range            = "443"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  target                = google_compute_region_target_https_proxy.management.self_link
  ip_address            = google_compute_address.regional_management_lb.address
}
