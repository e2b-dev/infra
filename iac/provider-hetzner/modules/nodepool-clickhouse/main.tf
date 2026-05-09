/**
 * Hetzner Nodepool — ClickHouse
 *
 * Provisions ClickHouse cluster nodes with Hetzner Cloud Volume for hot data.
 * 1:1 with provider-aws/modules/nodepool-clickhouse, but uses Hetzner Object
 * Storage (S3-compat) for cold-tier backup instead of AWS S3.
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

  user_data = templatefile("${local.scripts_path}/start-clickhouse.sh", {
    NODE_POOL                    = var.node_pool_name
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    CLUSTER_TAG_VALUE            = var.cluster_tag_value
    SCRIPTS_BUCKET               = var.setup_bucket_name
    CLICKHOUSE_BACKUPS_BUCKET    = var.clickhouse_backups_bucket
    OBJECT_STORE_URL             = var.object_store_url
    OBJECT_STORE_ACCESS_KEY      = var.object_store_access_key
    OBJECT_STORE_SECRET_KEY      = var.object_store_secret_key
    CONSUL_TOKEN                 = var.consul_acl_token
    CONSUL_GOSSIP_ENCRYPTION_KEY = var.consul_gossip_encryption_key
    NOMAD_TOKEN                  = var.nomad_acl_token
    DATA_VOLUME_DEVICE           = "/dev/disk/by-id/scsi-0HC_Volume_*"
    HOSTNAME_SUFFIX              = "${var.prefix}clickhouse"
  })
}

# ─────────────────────────── Snapshot Lookup ───────────────────────────

data "hcloud_image" "clickhouse" {
  count = var.image_id == "" ? 1 : 0

  with_selector     = "purpose=clickhouse,family=${var.image_family_prefix}"
  most_recent       = true
  with_architecture = "x86"
}

locals {
  resolved_image_id = var.image_id != "" ? var.image_id : tostring(data.hcloud_image.clickhouse[0].id)
}

# ─────────────────────────── Cloud Servers ───────────────────────────

resource "hcloud_server" "clickhouse" {
  count = var.cluster_size

  name        = "${var.prefix}clickhouse-${count.index + 1}"
  server_type = var.server_type
  image       = local.resolved_image_id
  location    = var.location
  ssh_keys    = var.ssh_key_ids

  user_data = local.user_data

  firewall_ids = var.firewall_ids

  network {
    network_id = var.network_id
    ip         = cidrhost(var.subnet_cidr, var.subnet_offset + count.index + 1)
  }

  labels = merge(var.common_labels, {
    component = "nodepool"
    role      = "clickhouse"
    pool      = var.node_pool_name
  })

  delete_protection  = !var.allow_force_destroy
  rebuild_protection = !var.allow_force_destroy

  lifecycle {
    ignore_changes = [user_data]
  }
}

# ─────────────────────────── Hot-Data Volume per Node ───────────────────────────

resource "hcloud_volume" "clickhouse_data" {
  count = var.cluster_size

  name     = "${var.prefix}clickhouse-data-${count.index + 1}"
  size     = var.data_volume_size_gb
  format   = "ext4"
  location = var.location

  labels = merge(var.common_labels, {
    component = "volume"
    role      = "clickhouse-data"
    server    = "${var.prefix}clickhouse-${count.index + 1}"
  })

  delete_protection = !var.allow_force_destroy
}

resource "hcloud_volume_attachment" "clickhouse_data" {
  count = var.cluster_size

  volume_id = hcloud_volume.clickhouse_data[count.index].id
  server_id = hcloud_server.clickhouse[count.index].id
  automount = true
}
