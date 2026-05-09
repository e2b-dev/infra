/**
 * Hetzner Cloud Load Balancer Module
 *
 * 1:1 functional replacement for provider-aws/alb.tf.
 *
 * Architecture mapping (AWS ALB → Hetzner Cloud LB):
 *
 *   AWS aws_lb (ingress)            → hcloud_load_balancer.main
 *   AWS aws_lb_listener (HTTP:80)   → hcloud_load_balancer_service (HTTP redirect)
 *   AWS aws_lb_listener (HTTPS:443) → hcloud_load_balancer_service (HTTPS, terminates TLS)
 *   AWS aws_lb_listener_rule grpc   → separate service on TCP:8443 (gRPC L4 passthrough)
 *   AWS aws_lb_listener_rule nomad  → service on TCP:4646 (Nomad UI)
 *   AWS aws_lb_target_group         → hcloud_load_balancer_target (label_selector)
 *
 * Hetzner Cloud LB differences:
 *   - No "listener rules" — each port-mapping is a separate Service.
 *   - Targets via label_selector (not target-group ARNs) — labels match
 *     against the nodepool labels set by NX.2.3.
 *   - HTTPS termination uses Hetzner Cloud Certificate (NX.2.2 cert module).
 *   - gRPC traffic uses TCP-protocol service (Layer 4 passthrough) since
 *     Hetzner LB only supports HTTP/2 via TLS-termination.
 */

terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.51.0"
    }
  }
}

# ─────────────────────────── Cloud Load Balancer ───────────────────────────

resource "hcloud_load_balancer" "main" {
  name               = "${var.prefix}ingress"
  load_balancer_type = var.lb_type
  location           = var.location
  algorithm {
    type = var.algorithm
  }

  labels = merge(var.common_labels, {
    component = "load-balancer"
    role      = "ingress"
  })

  delete_protection = !var.allow_force_destroy
}

# ─────────────────────────── Network Attachment ───────────────────────────

resource "hcloud_load_balancer_network" "main" {
  load_balancer_id        = hcloud_load_balancer.main.id
  network_id              = var.network_id
  ip                      = cidrhost(var.subnet_cidr, var.lb_subnet_offset)
  enable_public_interface = true
}

# ─────────────────────────── Service: HTTPS (443) — Web Ingress (HTTP1) ───────────────────────────

resource "hcloud_load_balancer_service" "https_web" {
  load_balancer_id = hcloud_load_balancer.main.id
  protocol         = "https"
  listen_port      = 443
  destination_port = var.ingress_port

  http {
    certificates    = [var.certificate_id]
    redirect_http   = false
    sticky_sessions = false
  }

  health_check {
    protocol = "http"
    port     = var.ingress_port
    interval = 5
    timeout  = 3
    retries  = 2
    http {
      path         = "/ping"
      status_codes = ["200"]
    }
  }
}

# ─────────────────────────── Service: HTTP (80) — Redirect to HTTPS ───────────────────────────

resource "hcloud_load_balancer_service" "http_redirect" {
  load_balancer_id = hcloud_load_balancer.main.id
  protocol         = "http"
  listen_port      = 80
  destination_port = var.ingress_port

  http {
    redirect_http = true
  }

  health_check {
    protocol = "http"
    port     = var.ingress_port
    interval = 10
    timeout  = 3
    retries  = 2
    http {
      path = "/ping"
    }
  }
}

# ─────────────────────────── Service: TCP (8443) — gRPC L4 Passthrough ───────────────────────────

resource "hcloud_load_balancer_service" "grpc" {
  count = var.enable_grpc ? 1 : 0

  load_balancer_id = hcloud_load_balancer.main.id
  protocol         = "tcp"
  listen_port      = var.grpc_listen_port
  destination_port = var.grpc_destination_port

  health_check {
    protocol = "tcp"
    port     = var.grpc_destination_port
    interval = 5
    timeout  = 3
    retries  = 2
  }
}

# ─────────────────────────── Service: TCP (4646) — Nomad UI ───────────────────────────

resource "hcloud_load_balancer_service" "nomad" {
  count = var.enable_nomad_listener ? 1 : 0

  load_balancer_id = hcloud_load_balancer.main.id
  protocol         = "tcp"
  listen_port      = var.nomad_listen_port
  destination_port = var.nomad_destination_port

  health_check {
    protocol = "http"
    port     = var.nomad_destination_port
    interval = 10
    timeout  = 5
    retries  = 3
    http {
      path         = "/v1/status/peers"
      status_codes = ["200"]
    }
  }
}

# ─────────────────────────── Targets via Label-Selector ───────────────────────────
# Targets are dynamically attached by matching labels — no static target-group needed.
# Ingress nodepool servers (NX.2.3 nodepool-api or dedicated ingress pool) are labeled
# with role=ingress, and the LB picks them up automatically.

resource "hcloud_load_balancer_target" "ingress_pool" {
  type             = "label_selector"
  load_balancer_id = hcloud_load_balancer.main.id
  label_selector   = "role=ingress,prefix=${trimsuffix(var.prefix, "-")}"
  use_private_ip   = true
}

resource "hcloud_load_balancer_target" "control_server_pool" {
  count = var.enable_nomad_listener ? 1 : 0

  type             = "label_selector"
  load_balancer_id = hcloud_load_balancer.main.id
  label_selector   = "role=control-server,prefix=${trimsuffix(var.prefix, "-")}"
  use_private_ip   = true
}
