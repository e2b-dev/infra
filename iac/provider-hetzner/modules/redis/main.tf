/**
 * Hetzner Redis Module
 *
 * Hetzner does NOT offer a managed Redis service (vs AWS ElastiCache /
 * GCP Memorystore). We provision a Cloud Server running Redis 7 with
 * persistence (AOF + RDB) and optional Sentinel for HA.
 *
 * 1:1 functional replacement for provider-aws/modules/redis.
 *
 * Default sizing: cx22 (2vCPU/4GB) for dev, cpx31 (4vCPU/8GB) for prod.
 * Replication: replica_size > 0 spawns N replicas + Sentinel quorum.
 */

terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.51.0"
    }
  }
}

locals {
  scripts_path = var.scripts_path != "" ? var.scripts_path : "${path.module}/scripts"
}

# ─────────────────────────── Snapshot Lookup ───────────────────────────

data "hcloud_image" "redis" {
  count = var.image_id == "" ? 1 : 0

  with_selector     = "purpose=redis,family=${var.image_family_prefix}"
  most_recent       = true
  with_architecture = "x86"
}

locals {
  resolved_image_id = var.image_id != "" ? var.image_id : tostring(data.hcloud_image.redis[0].id)
}

# ─────────────────────────── Primary Redis Server ───────────────────────────

resource "hcloud_server" "primary" {
  name        = "${var.prefix}redis-primary"
  server_type = var.server_type
  image       = local.resolved_image_id
  location    = var.location
  ssh_keys    = var.ssh_key_ids

  user_data = templatefile("${local.scripts_path}/start-redis.sh", {
    REDIS_ROLE       = "master"
    REDIS_PORT       = var.port
    REDIS_AUTH_TOKEN = var.auth_token
    PERSIST_DIR      = "/var/lib/redis"
    HOSTNAME_SUFFIX  = "${var.prefix}redis-primary"
  })

  firewall_ids = var.firewall_ids

  network {
    network_id = var.network_id
    ip         = cidrhost(var.subnet_cidr, var.subnet_offset)
  }

  labels = merge(var.common_labels, {
    component = "redis"
    role      = "redis-primary"
  })

  delete_protection  = !var.allow_force_destroy
  rebuild_protection = !var.allow_force_destroy

  lifecycle {
    ignore_changes = [user_data]
  }
}

# ─────────────────────────── Persistence Volume ───────────────────────────

resource "hcloud_volume" "primary_data" {
  name     = "${var.prefix}redis-primary-data"
  size     = var.data_volume_size_gb
  format   = "ext4"
  location = var.location

  labels = merge(var.common_labels, {
    component = "volume"
    role      = "redis-data"
  })

  delete_protection = !var.allow_force_destroy
}

resource "hcloud_volume_attachment" "primary_data" {
  volume_id = hcloud_volume.primary_data.id
  server_id = hcloud_server.primary.id
  automount = true
}

# ─────────────────────────── Replicas (Optional HA) ───────────────────────────

resource "hcloud_server" "replica" {
  count = var.replica_size

  name        = "${var.prefix}redis-replica-${count.index + 1}"
  server_type = var.server_type
  image       = local.resolved_image_id
  location    = var.location
  ssh_keys    = var.ssh_key_ids

  user_data = templatefile("${local.scripts_path}/start-redis.sh", {
    REDIS_ROLE         = "replica"
    REDIS_PORT         = var.port
    REDIS_AUTH_TOKEN   = var.auth_token
    REDIS_PRIMARY_HOST = tolist(hcloud_server.primary.network)[0].ip
    REDIS_PRIMARY_PORT = var.port
    PERSIST_DIR        = "/var/lib/redis"
    HOSTNAME_SUFFIX    = "${var.prefix}redis-replica-${count.index + 1}"
  })

  firewall_ids = var.firewall_ids

  network {
    network_id = var.network_id
    ip         = cidrhost(var.subnet_cidr, var.subnet_offset + count.index + 1)
  }

  labels = merge(var.common_labels, {
    component = "redis"
    role      = "redis-replica"
    primary   = hcloud_server.primary.name
  })

  delete_protection  = !var.allow_force_destroy
  rebuild_protection = !var.allow_force_destroy

  lifecycle {
    ignore_changes = [user_data]
  }
}

resource "hcloud_volume" "replica_data" {
  count = var.replica_size

  name     = "${var.prefix}redis-replica-${count.index + 1}-data"
  size     = var.data_volume_size_gb
  format   = "ext4"
  location = var.location

  labels = merge(var.common_labels, {
    component = "volume"
    role      = "redis-replica-data"
  })

  delete_protection = !var.allow_force_destroy
}

resource "hcloud_volume_attachment" "replica_data" {
  count = var.replica_size

  volume_id = hcloud_volume.replica_data[count.index].id
  server_id = hcloud_server.replica[count.index].id
  automount = true
}
