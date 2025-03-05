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
      groups = [{ group = var.build_instance_group }]
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
    consul = {
      protocol                        = "HTTP"
      port                            = 80
      port_name                       = "consul"
      timeout_sec                     = 10
      connection_draining_timeout_sec = 1
      http_health_check = {
        request_path = "/v1/status/peers"
        port         = 8500
      }
      groups = [
      { group = var.server_instance_group }]
    }
  }
  health_checked_backends = { for backend_index, backend_value in local.backends : backend_index => backend_value }
}

# ======== IP ADDRESSES ====================

resource "google_compute_global_address" "orch_logs_ip" {
  name = "${var.prefix}logs-ip"
}


# ======== CLOUDFLARE ====================

data "cloudflare_zone" "domain" {
  name = var.domain_name
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
  name    = "*"
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
    hosts        = concat(["consul.${var.domain_name}"], [for d in var.additional_domains : "consul.${d}"])
    path_matcher = "consul-paths"
  }

  host_rule {
    hosts        = concat(["*.${var.domain_name}"], [for d in var.additional_domains : "*.${d}"])
    path_matcher = "session-paths"
  }

  path_matcher {
    name            = "api-paths"
    default_service = google_compute_backend_service.default["api"].self_link
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

  path_matcher {
    name            = "consul-paths"
    default_service = google_compute_backend_service.default["consul"].self_link
  }
}

### IPv4 block ###
resource "google_compute_target_https_proxy" "default" {
  name    = "${var.prefix}https-proxy"
  url_map = google_compute_url_map.orch_map.self_link

  certificate_map = "//certificatemanager.googleapis.com/${google_certificate_manager_certificate_map.certificate_map.id}"
}

resource "google_compute_global_forwarding_rule" "https" {
  provider              = google-beta
  name                  = "${var.prefix}forwarding-rule-https"
  target                = google_compute_target_https_proxy.default.self_link
  load_balancing_scheme = "EXTERNAL_MANAGED"
  port_range            = "443"
  labels                = var.labels
}


resource "google_compute_backend_service" "default" {
  provider = google-beta
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
    enable = var.environment == "prod"
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
  provider = google-beta
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
  provider = google-beta
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

resource "google_compute_firewall" "default-hc" {
  name    = "${var.prefix}load-balancer-hc"
  network = var.network_name
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
}

module "gce_lb_http_logs" {
  source            = "GoogleCloudPlatform/lb-http/google"
  version           = "~> 9.3"
  name              = "${var.prefix}external-logs-endpoint"
  project           = var.gcp_project_id
  address           = google_compute_global_address.orch_logs_ip.address
  create_address    = false
  target_tags       = [var.cluster_tag_name]
  firewall_networks = [var.network_name]

  labels = var.labels
  backends = {
    default = {
      description                     = null
      protocol                        = "HTTP"
      port                            = var.logs_proxy_port.port
      port_name                       = var.logs_proxy_port.name
      timeout_sec                     = 20
      connection_draining_timeout_sec = 1
      enable_cdn                      = false
      session_affinity                = null
      affinity_cookie_ttl_sec         = null
      custom_request_headers          = null
      custom_response_headers         = null
      security_policy                 = google_compute_security_policy.disable-bots-log-collector.self_link

      health_check = {
        check_interval_sec  = null
        timeout_sec         = null
        healthy_threshold   = null
        unhealthy_threshold = null
        request_path        = var.logs_health_proxy_port.health_path
        port                = var.logs_health_proxy_port.port
        host                = null
        logging             = null
      }

      log_config = {
        enable      = false
        sample_rate = 0.0
      }

      groups = [
        {
          group                        = var.client_instance_group
          balancing_mode               = null
          capacity_scaler              = null
          description                  = null
          max_connections              = null
          max_connections_per_instance = null
          max_connections_per_endpoint = null
          max_rate                     = null
          max_rate_per_instance        = null
          max_rate_per_endpoint        = null
          max_utilization              = null
        },
      ]

      iap_config = {
        enable               = false
        oauth2_client_id     = ""
        oauth2_client_secret = ""
      }
    }
  }
}

# Firewalls
resource "google_compute_firewall" "orch_firewall_ingress" {
  name    = "${var.prefix}${var.cluster_tag_name}-firewall-ingress"
  network = var.network_name

  allow {
    protocol = "tcp"
    ports    = ["80", "8080", var.nomad_port, "3001", "3002", "3003", "30006", "44313", "50001", "8500"]
  }

  priority = 999

  direction     = "INGRESS"
  target_tags   = [var.cluster_tag_name]
  source_ranges = ["130.211.0.0/22", "35.191.0.0/16"]
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
  provider        = google-beta
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
      count        = var.domain_name == "e2b.dev" ? 5000 : 5000
      interval_sec = 30
    }
  }

  description = "Sandbox creation per API key"
}


resource "google_compute_security_policy_rule" "api-throttling-ip" {
  security_policy = google_compute_security_policy.default["api"].name
  provider        = google-beta
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
      count        = var.domain_name == "e2b.dev" ? 20000 : 20000
      interval_sec = 30
    }
  }

  description = "Requests to API from IP address"
}

resource "google_compute_security_policy_rule" "sandbox-throttling-host" {
  security_policy = google_compute_security_policy.default["session"].name
  provider        = google-beta
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
  provider        = google-beta
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
      count        = var.domain_name == "e2b.dev" ? 40000 : 40000
      interval_sec = 60
    }
  }

  description = "Requests to sandboxes from IP address"
}

resource "google_compute_security_policy_rule" "disable-consul" {
  security_policy = google_compute_security_policy.default["consul"].name
  provider        = google-beta
  action          = "deny(403)"
  priority        = "1"
  description     = "Disable all requests to Consul"
  match {
    versioned_expr = "SRC_IPS_V1"
    config {
      src_ip_ranges = ["*"]
    }
  }
}



resource "google_compute_security_policy" "disable-bots-log-collector" {
  name     = "disable-bots-log-collector"
  provider = google-beta

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
