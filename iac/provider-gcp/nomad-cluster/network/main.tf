terraform {
  required_providers {
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
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
      # TODO: (2025-10-01) - this should be only api instance group, but keeping this here for a migration period (at least until 2025-10-15)
      groups = [
        { group = var.api_instance_group },
        { group = var.build_instance_group },
      ]
    }
  }
  health_checked_backends = { for backend_index, backend_value in local.backends : backend_index => backend_value }
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
    ports    = [var.nomad_port]
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
