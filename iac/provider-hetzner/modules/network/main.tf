/**
 * Hetzner Network Module
 *
 * Provisions the Hetzner Cloud Network with subnets and Cloud Firewalls.
 * Mirrors provider-aws/modules/network and provider-gcp/nomad-cluster/network.
 *
 * Components:
 *   1. Cloud Network — top-level private network (10.0.0.0/8)
 *   2. Cloud Subnet — for Cloud Servers (10.0.1.0/24)
 *   3. vSwitch Subnet — for Cloud↔Robot bridging (10.10.0.0/24, optional)
 *   4. Cloud Firewall: public-ingress (HTTPS/HTTP/SSH-mgmt only)
 *   5. Cloud Firewall: cluster-internal (Nomad/Consul/Redis/gRPC traffic)
 *   6. Cloud Firewall: sandbox-egress (DNS+HTTP/S, optional internal allowlist)
 */

terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.51.0"
    }
  }
}

# ─────────────────────────── Cloud Network ───────────────────────────

resource "hcloud_network" "main" {
  name                     = "${var.prefix}network"
  ip_range                 = var.cloud_cidr
  expose_routes_to_vswitch = var.vswitch_id != 0

  labels = merge(var.common_labels, {
    component = "network"
  })

  delete_protection = !var.allow_force_destroy
}

# ─────────────────────────── Cloud Subnet (Cloud Servers) ───────────────────────────

resource "hcloud_network_subnet" "cloud" {
  network_id   = hcloud_network.main.id
  type         = "cloud"
  network_zone = var.network_zone
  ip_range     = var.cloud_subnet_cidr
}

# ─────────────────────────── vSwitch Subnet (Cloud↔Robot Bridge) ───────────────────────────
# Only created when vswitch_id != 0 (Robot integration enabled).

resource "hcloud_network_subnet" "vswitch" {
  count = var.vswitch_id != 0 ? 1 : 0

  network_id   = hcloud_network.main.id
  type         = "vswitch"
  network_zone = var.network_zone
  ip_range     = var.vswitch_cidr
  vswitch_id   = var.vswitch_id
}

# ─────────────────────────── Cloud Firewall: Public Ingress ───────────────────────────
# Allows public HTTPS/HTTP into the cluster (for Hetzner Cloud LB and direct ingress).

resource "hcloud_firewall" "public_ingress" {
  name = "${var.prefix}fw-public-ingress"

  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "443"
    source_ips  = ["0.0.0.0/0", "::/0"]
    description = "HTTPS public ingress"
  }

  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "80"
    source_ips  = ["0.0.0.0/0", "::/0"]
    description = "HTTP redirect to HTTPS"
  }

  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "22"
    source_ips  = var.management_cidrs
    description = "SSH from management CIDRs only"
  }

  rule {
    direction   = "in"
    protocol    = "icmp"
    source_ips  = ["0.0.0.0/0", "::/0"]
    description = "ICMP echo (ping)"
  }

  labels = merge(var.common_labels, {
    component = "firewall"
    role      = "public-ingress"
  })
}

# ─────────────────────────── Cloud Firewall: Cluster Internal ───────────────────────────
# Allows Nomad/Consul/Redis/internal-gRPC traffic between cluster nodes.

resource "hcloud_firewall" "cluster_internal" {
  name = "${var.prefix}fw-cluster-internal"

  # Nomad
  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "4646-4648"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "Nomad HTTP/RPC/Serf"
  }

  rule {
    direction   = "in"
    protocol    = "udp"
    port        = "4648"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "Nomad Serf (UDP)"
  }

  # Consul
  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "8300-8302"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "Consul Server RPC + Serf"
  }

  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "8500"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "Consul HTTP API"
  }

  rule {
    direction   = "in"
    protocol    = "udp"
    port        = "8301-8302"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "Consul Serf (UDP)"
  }

  # Redis
  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "6379"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "Redis"
  }

  # Orchestrator gRPC
  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "5008-5009"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "Orchestrator gRPC + internal API gRPC"
  }

  # ClickHouse
  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "9000"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "ClickHouse native protocol"
  }

  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "8123"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "ClickHouse HTTP"
  }

  # Loki
  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "3100"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "Loki HTTP"
  }

  # OTEL collector
  rule {
    direction   = "in"
    protocol    = "tcp"
    port        = "4317-4318"
    source_ips  = [var.cloud_subnet_cidr, var.vswitch_cidr]
    description = "OTEL gRPC + HTTP"
  }

  labels = merge(var.common_labels, {
    component = "firewall"
    role      = "cluster-internal"
  })
}

# ─────────────────────────── Cloud Firewall: Sandbox Egress ───────────────────────────
# Restricts what sandbox VMs can reach outside themselves.
# Default: allow DNS + HTTP/HTTPS to public Internet, deny private RFC1918 unless allowlisted.

resource "hcloud_firewall" "sandbox_egress" {
  name = "${var.prefix}fw-sandbox-egress"

  rule {
    direction       = "out"
    protocol        = "tcp"
    port            = "53"
    destination_ips = ["0.0.0.0/0", "::/0"]
    description     = "DNS over TCP"
  }

  rule {
    direction       = "out"
    protocol        = "udp"
    port            = "53"
    destination_ips = ["0.0.0.0/0", "::/0"]
    description     = "DNS over UDP"
  }

  rule {
    direction       = "out"
    protocol        = "tcp"
    port            = "80"
    destination_ips = ["0.0.0.0/0", "::/0"]
    description     = "HTTP outbound"
  }

  rule {
    direction       = "out"
    protocol        = "tcp"
    port            = "443"
    destination_ips = ["0.0.0.0/0", "::/0"]
    description     = "HTTPS outbound"
  }

  dynamic "rule" {
    for_each = length(var.allow_sandbox_internal_cidrs) > 0 ? [1] : []
    content {
      direction       = "out"
      protocol        = "tcp"
      port            = "any"
      destination_ips = var.allow_sandbox_internal_cidrs
      description     = "Sandbox-allowed internal CIDRs"
    }
  }

  labels = merge(var.common_labels, {
    component = "firewall"
    role      = "sandbox-egress"
  })
}
