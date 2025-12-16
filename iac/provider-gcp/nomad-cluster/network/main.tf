terraform {
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.19.0"
    }
  }
}

data "google_secret_manager_secret_version" "cloudflare_api_token" {
  secret = var.cloudflare_api_token_secret_name
}

provider "cloudflare" {
  api_token = data.google_secret_manager_secret_version.cloudflare_api_token.secret_data
}

locals {
  domain_map = { for d in var.additional_domains : replace(d, ".", "-") => d }

  parts        = split(".", var.domain_name)
  is_subdomain = length(local.parts) > 2
  // Take everything except last 2 parts
  subdomain = local.is_subdomain ? join(".", slice(local.parts, 0, length(local.parts) - 2)) : ""
  // Take last 2 parts (1 dot)
  root_domain = local.is_subdomain ? join(".", slice(local.parts, length(local.parts) - 2, length(local.parts))) : var.domain_name

  backends = {
    session = {
      protocol                        = "HTTP"
      port                            = var.client_proxy_port.port
      port_name                       = var.client_proxy_port.name
      timeout_sec                     = 86400
      connection_draining_timeout_sec = 1
      http_health_check = {
        request_path       = var.client_proxy_health_port.path
        port               = var.client_proxy_health_port.port
        timeout_sec        = 3
        check_interval_sec = 3
      }
      groups = [{ group = var.api_instance_group }]
    }
    api = {
      protocol                        = "HTTP"
      port                            = var.api_port.port
      port_name                       = var.api_port.name
      timeout_sec                     = 65
      connection_draining_timeout_sec = 1
      http_health_check = {
        request_path       = var.api_port.health_path
        port               = var.api_port.port
        timeout_sec        = 3
        check_interval_sec = 3
      }
      groups = [{ group = var.api_instance_group }]
    }
    docker-reverse-proxy = {
      protocol                        = "HTTP"
      port                            = var.docker_reverse_proxy_port.port
      port_name                       = var.docker_reverse_proxy_port.name
      timeout_sec                     = 30
      connection_draining_timeout_sec = 1
      http_health_check = {
        request_path = var.docker_reverse_proxy_port.health_path
        port         = var.docker_reverse_proxy_port.port
      }
      groups = [
        { group = var.api_instance_group },
      ]
    }
    nomad = {
      protocol                        = "HTTP"
      port                            = 80
      port_name                       = "nomad"
      timeout_sec                     = 10
      connection_draining_timeout_sec = 1
      http_health_check = {
        request_path = "/v1/status/peers"
        port         = var.nomad_port
      }
      groups = [{ group = var.server_instance_group }]
    }
  }
  health_checked_backends = { for backend_index, backend_value in local.backends : backend_index => backend_value }
}

# ======== CLOUDFLARE ====================

data "cloudflare_zone" "domain" {
  name = local.root_domain
}

resource "cloudflare_record" "dns_auth" {
  zone_id = data.cloudflare_zone.domain.id
  name    = google_certificate_manager_dns_authorization.dns_auth.dns_resource_record[0].name
  value   = google_certificate_manager_dns_authorization.dns_auth.dns_resource_record[0].data
  type    = google_certificate_manager_dns_authorization.dns_auth.dns_resource_record[0].type
  ttl     = 3600
}

resource "cloudflare_record" "a_star" {
  zone_id = data.cloudflare_zone.domain.id
  name    = local.is_subdomain ? "*.${local.subdomain}" : "*"
  value   = google_compute_global_forwarding_rule.https.ip_address
  type    = "A"
  comment = var.gcp_project_id
}

data "cloudflare_zone" "domains_additional" {
  for_each = local.domain_map
  name     = each.value
}


resource "cloudflare_record" "dns_auth_additional" {
  for_each = local.domain_map
  zone_id  = data.cloudflare_zone.domains_additional[each.key].id
  name     = google_certificate_manager_dns_authorization.dns_auth_additional[each.key].dns_resource_record[0].name
  value    = google_certificate_manager_dns_authorization.dns_auth_additional[each.key].dns_resource_record[0].data
  type     = google_certificate_manager_dns_authorization.dns_auth_additional[each.key].dns_resource_record[0].type
  ttl      = 3600
}


resource "cloudflare_record" "a_star_additional" {
  for_each = local.domain_map
  zone_id  = data.cloudflare_zone.domains_additional[each.key].id
  name     = "*"
  value    = google_compute_global_forwarding_rule.https.ip_address
  type     = "A"
  comment  = var.gcp_project_id
}

# =======================================

# Certificate
resource "google_certificate_manager_dns_authorization" "dns_auth" {
  name        = "${var.prefix}dns-auth"
  description = "The default dns auth"
  domain      = var.domain_name
  labels      = var.labels
}

# Certificate
resource "google_certificate_manager_dns_authorization" "dns_auth_additional" {
  for_each    = local.domain_map
  name        = "${var.prefix}dns-auth-${each.key}"
  description = "The default dns auth"
  domain      = each.value
  labels      = var.labels
}

resource "google_certificate_manager_certificate" "root_cert" {
  name        = "${var.prefix}root-cert"
  description = "The wildcard cert"
  managed {
    domains = [var.domain_name, "*.${var.domain_name}"]
    dns_authorizations = [
      google_certificate_manager_dns_authorization.dns_auth.id
    ]
  }
  labels = var.labels
}

resource "google_certificate_manager_certificate" "root_cert_additional" {
  for_each    = local.domain_map
  name        = "${var.prefix}root-cert-${each.key}"
  description = "The wildcard cert"
  managed {
    domains = [each.value, "*.${each.value}"]
    dns_authorizations = [
      google_certificate_manager_dns_authorization.dns_auth_additional[each.key].id
    ]
  }
  labels = var.labels
}

resource "google_certificate_manager_certificate_map" "certificate_map" {
  name        = "${var.prefix}cert-map"
  description = "${var.domain_name} certificate map"
  labels      = var.labels
}

resource "google_certificate_manager_certificate_map_entry" "top_level_map_entry" {
  name        = "${var.prefix}top-level"
  description = "Top level map entry"
  map         = google_certificate_manager_certificate_map.certificate_map.name
  labels      = var.labels


  certificates = [google_certificate_manager_certificate.root_cert.id]
  hostname     = var.domain_name
}

resource "google_certificate_manager_certificate_map_entry" "top_level_map_entry_additional" {
  for_each    = local.domain_map
  name        = "${var.prefix}top-level-${each.key}"
  description = "Top level map entry"
  map         = google_certificate_manager_certificate_map.certificate_map.name
  labels      = var.labels


  certificates = [google_certificate_manager_certificate.root_cert_additional[each.key].id]
  hostname     = each.value
}


resource "google_certificate_manager_certificate_map_entry" "subdomains_map_entry" {
  name        = "${var.prefix}subdomains"
  description = "Subdomains map entry"
  map         = google_certificate_manager_certificate_map.certificate_map.name
  labels      = var.labels

  certificates = [google_certificate_manager_certificate.root_cert.id]
  hostname     = "*.${var.domain_name}"
}

resource "google_certificate_manager_certificate_map_entry" "subdomains_map_entry_additional" {
  for_each    = local.domain_map
  name        = "${var.prefix}subdomains-${each.key}"
  description = "Subdomains map entry"
  map         = google_certificate_manager_certificate_map.certificate_map.name
  labels      = var.labels

  certificates = [
    google_certificate_manager_certificate.root_cert_additional[each.key].id
  ]
  hostname = "*.${each.value}"
}

# Load balancers
resource "google_compute_url_map" "orch_map" {
  name            = "${var.prefix}orch-map"
  default_service = google_compute_backend_service.default["nomad"].self_link

  host_rule {
    hosts        = concat(["api.${var.domain_name}"], [for d in var.additional_domains : "api.${d}"])
    path_matcher = "api-paths"
  }

  host_rule {
    hosts        = concat(["docker.${var.domain_name}"], [for d in var.additional_domains : "docker.${d}"])
    path_matcher = "docker-reverse-proxy-paths"
  }

  host_rule {
    hosts        = concat(["nomad.${var.domain_name}"], [for d in var.additional_domains : "nomad.${d}"])
    path_matcher = "nomad-paths"
  }

  host_rule {
    hosts        = concat(["*.${var.domain_name}"], [for d in var.additional_domains : "*.${d}"])
    path_matcher = "session-paths"
  }

  path_matcher {
    name            = "api-paths"
    default_service = google_compute_backend_service.default["api"].self_link

    dynamic "path_rule" {
      for_each = var.additional_api_path_rules
      content {
        paths   = path_rule.value.paths
        service = path_rule.value.service_id
      }
    }
  }

  path_matcher {
    name            = "docker-reverse-proxy-paths"
    default_service = google_compute_backend_service.default["docker-reverse-proxy"].self_link
  }

  path_matcher {
    name            = "session-paths"
    default_service = google_compute_backend_service.default["session"].self_link
  }

  path_matcher {
    name            = "nomad-paths"
    default_service = google_compute_backend_service.default["nomad"].self_link

    path_rule {
      paths   = ["/v1/metrics"]
      service = google_compute_backend_service.default["nomad"].self_link
      route_action {
        url_rewrite {
          // We prevent access to these routes by rewriting the path to /
          path_prefix_rewrite = "/"
          host_rewrite        = "nomad.${var.domain_name}"
        }
      }
    }
  }
}

### IPv4 block ###
resource "google_compute_ssl_policy" "default" {
  name            = "${var.prefix}https-proxy-ssl-policy"
  profile         = "MODERN"
  min_tls_version = "TLS_1_2"
}

resource "google_compute_target_https_proxy" "default" {
  name    = "${var.prefix}https-proxy"
  url_map = google_compute_url_map.orch_map.self_link

  ssl_policy = google_compute_ssl_policy.default.self_link

  certificate_map = "//certificatemanager.googleapis.com/${google_certificate_manager_certificate_map.certificate_map.id}"
}

resource "google_compute_global_forwarding_rule" "https" {
  name                  = "${var.prefix}forwarding-rule-https"
  target                = google_compute_target_https_proxy.default.self_link
  load_balancing_scheme = "EXTERNAL_MANAGED"
  port_range            = "443"
  labels                = var.labels
}

resource "google_compute_backend_service" "default" {
  for_each = local.backends

  name = "${var.prefix}backend-${each.key}"

  port_name = lookup(each.value, "port_name", "http")
  protocol  = lookup(each.value, "protocol", "HTTP")

  timeout_sec                     = lookup(each.value, "timeout_sec")
  connection_draining_timeout_sec = 1
  compression_mode                = "DISABLED"

  load_balancing_scheme = "EXTERNAL_MANAGED"
  health_checks         = [google_compute_health_check.default[each.key].self_link]

  security_policy = google_compute_security_policy.default[each.key].self_link

  log_config {
    enable = var.environment != "dev"
  }

  dynamic "backend" {
    for_each = toset(each.value["groups"])
    content {
      group = lookup(backend.value, "group")
    }
  }

  depends_on = [
    google_compute_health_check.default
  ]
}

resource "google_compute_health_check" "default" {
  for_each = local.health_checked_backends
  name     = "${var.prefix}hc-${each.key}"

  check_interval_sec  = lookup(each.value["http_health_check"], "check_interval_sec", 5)
  timeout_sec         = lookup(each.value["http_health_check"], "timeout_sec", 5)
  healthy_threshold   = lookup(each.value["http_health_check"], "healthy_threshold", 2)
  unhealthy_threshold = lookup(each.value["http_health_check"], "unhealthy_threshold", 2)

  log_config {
    enable = false
  }

  dynamic "http_health_check" {
    for_each = coalesce(lookup(each.value["http_health_check"], "protocol", null), each.value["protocol"]) == "HTTP" ? [
      {
        host               = lookup(each.value["http_health_check"], "host", null)
        request_path       = lookup(each.value["http_health_check"], "request_path", null)
        response           = lookup(each.value["http_health_check"], "response", null)
        port               = lookup(each.value["http_health_check"], "port", null)
        port_name          = lookup(each.value["http_health_check"], "port_name", null)
        proxy_header       = lookup(each.value["http_health_check"], "proxy_header", null)
        port_specification = lookup(each.value["http_health_check"], "port_specification", null)
      }
      ] : [
      {
        host               = lookup(each.value["http_health_check"], "host", null)
        request_path       = lookup(each.value["http_health_check"], "request_path", null)
        response           = lookup(each.value["http_health_check"], "response", null)
        port               = lookup(each.value["http_health_check"], "port", null)
        port_name          = lookup(each.value["http_health_check"], "port_name", null)
        proxy_header       = lookup(each.value["http_health_check"], "proxy_header", null)
        port_specification = lookup(each.value["http_health_check"], "port_specification", null)
      }
    ]

    content {
      host               = lookup(http_health_check.value, "host", null)
      request_path       = lookup(http_health_check.value, "request_path", null)
      response           = lookup(http_health_check.value, "response", null)
      port               = lookup(http_health_check.value, "port", null)
      port_name          = lookup(http_health_check.value, "port_name", null)
      proxy_header       = lookup(http_health_check.value, "proxy_header", null)
      port_specification = lookup(http_health_check.value, "port_specification", null)
    }
  }
}


resource "google_compute_security_policy" "default" {
  for_each = local.health_checked_backends
  name     = "${var.prefix}${each.key}"

  dynamic "adaptive_protection_config" {
    for_each = each.key == "api" ? [true] : []

    content {
      layer_7_ddos_defense_config {
        enable = true
      }
    }
  }
}

# Firewalls
resource "google_compute_firewall" "default-hc" {
  name    = "${var.prefix}load-balancer-hc"
  network = var.network_name
  # Load balancer health check IP ranges
  # https://cloud.google.com/load-balancing/docs/health-check-concepts
  source_ranges = [
    "130.211.0.0/22",
    "35.191.0.0/16"
  ]
  target_tags = [var.cluster_tag_name]

  priority = 999

  dynamic "allow" {
    for_each = local.health_checked_backends

    content {
      protocol = "tcp"
      ports    = [allow.value["http_health_check"].port]
    }
  }

  allow {
    protocol = "tcp"
    ports    = [var.ingress_port.port]
  }

  dynamic "allow" {
    for_each = toset(var.additional_ports)

    content {
      protocol = "tcp"
      ports    = [allow.value]
    }
  }
}

resource "google_compute_firewall" "client_proxy_firewall_ingress" {
  name    = "${var.prefix}${var.cluster_tag_name}-client-proxy-firewall-ingress"
  network = var.network_name

  allow {
    protocol = "tcp"
    ports    = ["3002"]
  }

  priority = 999

  direction   = "INGRESS"
  target_tags = [var.cluster_tag_name]
  # Load balancer health check IP ranges
  # https://cloud.google.com/load-balancing/docs/health-check-concepts
  source_ranges = ["130.211.0.0/22", "35.191.0.0/16"]
}

resource "google_compute_firewall" "internal_remote_connection_firewall_ingress" {
  name    = "${var.prefix}${var.cluster_tag_name}-internal-remote-connection-firewall-ingress"
  network = var.network_name

  allow {
    protocol = "tcp"
    ports    = ["22", "3389"]
  }

  priority = 900

  direction   = "INGRESS"
  target_tags = [var.cluster_tag_name]
  # https://googlecloudplatform.github.io/iap-desktop/setup-iap/
  source_ranges = var.environment == "dev" ? ["0.0.0.0/0"] : ["35.235.240.0/20"]
}

resource "google_compute_firewall" "remote_connection_firewall_ingress" {
  name    = "${var.prefix}${var.cluster_tag_name}-remote-connection-firewall-ingress"
  network = var.network_name

  deny {
    protocol = "tcp"
    ports    = ["22", "3389"]
  }


  #  Metadata fields can be found here: https://cloud.google.com/firewall/docs/firewall-rules-logging#log-format
  dynamic "log_config" {
    for_each = var.environment != "dev" ? [1] : []
    content {
      metadata = "EXCLUDE_ALL_METADATA"
    }
  }

  priority = 1000

  direction     = "INGRESS"
  target_tags   = [var.cluster_tag_name]
  source_ranges = ["0.0.0.0/0"]
}


resource "google_compute_firewall" "orch_firewall_egress" {
  name    = "${var.prefix}${var.cluster_tag_name}-firewall-egress"
  network = var.network_name

  allow {
    protocol = "all"
  }

  direction   = "EGRESS"
  target_tags = [var.cluster_tag_name]
}


# Security policy
resource "google_compute_security_policy_rule" "api-throttling-api-key" {
  security_policy = google_compute_security_policy.default["api"].name
  action          = "throttle"
  priority        = "300"
  match {
    expr {
      expression = "request.path == \"/sandboxes\" && request.method == \"POST\""
    }
  }

  rate_limit_options {
    conform_action = "allow"
    exceed_action  = "deny(429)"

    enforce_on_key_configs {
      enforce_on_key_name = "X-API-Key"
      enforce_on_key_type = "HTTP_HEADER"
    }

    rate_limit_threshold {
      count        = 1200
      interval_sec = 10
    }
  }

  description = "Sandbox creation per API key"
}


resource "google_compute_security_policy_rule" "api-throttling-ip" {
  security_policy = google_compute_security_policy.default["api"].name
  action          = "throttle"
  priority        = "500"
  match {
    versioned_expr = "SRC_IPS_V1"
    config {
      src_ip_ranges = ["*"]
    }
  }

  rate_limit_options {
    conform_action = "allow"
    exceed_action  = "deny(429)"

    enforce_on_key = ""

    enforce_on_key_configs {
      enforce_on_key_type = "IP"
    }

    rate_limit_threshold {
      count        = 20000
      interval_sec = 30
    }
  }

  description = "Requests to API from IP address"
}

resource "google_compute_security_policy_rule" "sandbox-throttling-host" {
  security_policy = google_compute_security_policy.default["session"].name
  description     = "WS envd connection requests per sandbox"

  action   = "throttle"
  priority = "300"
  match {
    expr {
      expression = "request.path == \"/ws\""
    }
  }

  rate_limit_options {
    conform_action = "allow"
    exceed_action  = "deny(429)"

    enforce_on_key_configs {
      enforce_on_key_name = "host"
      enforce_on_key_type = "HTTP_HEADER"
    }

    rate_limit_threshold {
      count        = 40
      interval_sec = 30
    }
  }
}

resource "google_compute_security_policy_rule" "sandbox-throttling-ip" {
  security_policy = google_compute_security_policy.default["session"].name
  action          = "throttle"
  priority        = "500"
  match {
    versioned_expr = "SRC_IPS_V1"
    config {
      src_ip_ranges = ["*"]
    }
  }

  rate_limit_options {
    conform_action = "allow"
    exceed_action  = "deny(429)"

    enforce_on_key = ""

    enforce_on_key_configs {
      enforce_on_key_type = "IP"
    }

    rate_limit_threshold {
      count        = 60000
      interval_sec = 60
    }
  }

  description = "Requests to sandboxes from IP address"
}

resource "google_compute_security_policy" "disable-bots-log-collector" {
  name = "disable-bots-log-collector"

  rule {
    action   = "allow"
    priority = "300"
    match {
      expr {
        expression = "request.path == \"/\" && request.method == \"POST\""
      }
    }

    description = "Allow POST requests  to / (collecting logs)"
  }

  rule {
    action      = "deny(403)"
    priority    = "2147483647"
    description = "Default rule, higher priority overrides it"
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
  }
}

# Cloud Router for NAT
resource "google_compute_router" "nat_router" {
  count   = var.api_use_nat ? 1 : 0
  name    = "${var.prefix}nat-router"
  network = var.network_name
  region  = var.gcp_region
}

# Static IP addresses for NAT (only created if explicit IPs not provided)
resource "google_compute_address" "nat_ips" {
  count  = var.api_use_nat && length(var.api_nat_ips) == 0 ? 2 : 0
  name   = "${var.prefix}nat-ip-${count.index + 1}"
  region = var.gcp_region
}

# Cloud NAT for API nodes
resource "google_compute_router_nat" "api_nat" {
  count                              = var.api_use_nat ? 1 : 0
  name                               = "${var.prefix}api-nat"
  router                             = google_compute_router.nat_router[0].name
  nat_ip_allocate_option             = "MANUAL_ONLY"
  nat_ips                            = length(var.api_nat_ips) > 0 ? var.api_nat_ips : google_compute_address.nat_ips[*].self_link
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"
  min_ports_per_vm                   = var.api_nat_min_ports_per_vm

  log_config {
    enable = true
    filter = "ERRORS_ONLY"
  }

  lifecycle {
    create_before_destroy = true
  }
}
