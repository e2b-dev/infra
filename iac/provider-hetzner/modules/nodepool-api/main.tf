/**
 * Hetzner Nodepool — API
 *
 * Provisions a pool of Hetzner Cloud Servers running the e2b API service
 * (orchestrator + envd + dashboard-api + agent-api). 1:1 with
 * provider-aws/modules/nodepool-api but Hetzner-native (no IAM, hcloud_server).
 *
 * Cloud-Init bootstraps Consul + Nomad agents and joins the cluster.
 * Run-Scripts (run-consul.sh, run-nomad.sh) are baked into the snapshot
 * by Packer-Hetzner (NX.2.6) and referenced by hash for cache-busting.
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

  user_data = templatefile("${local.scripts_path}/start-api.sh", {
    NODE_POOL                    = var.node_pool_name
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    CLUSTER_TAG_VALUE            = var.cluster_tag_value
    SCRIPTS_BUCKET               = var.setup_bucket_name
    OBJECT_STORE_URL             = var.object_store_url
    OBJECT_STORE_ACCESS_KEY      = var.object_store_access_key
    OBJECT_STORE_SECRET_KEY      = var.object_store_secret_key
    CONSUL_TOKEN                 = var.consul_acl_token
    CONSUL_GOSSIP_ENCRYPTION_KEY = var.consul_gossip_encryption_key
    CONSUL_DNS_REQUEST_TOKEN     = var.consul_dns_request_token
    NOMAD_TOKEN                  = var.nomad_acl_token
    LOKI_BUCKET                  = var.loki_bucket
    HOSTNAME_SUFFIX              = "${var.prefix}api"
  })
}

# ─────────────────────────── Snapshot Lookup ───────────────────────────
# Find the most recent Packer-built snapshot matching the API image family.
# When image_family_prefix is empty, callers must supply image_id directly.

data "hcloud_image" "api" {
  count = var.image_id == "" ? 1 : 0

  with_selector     = "purpose=api,family=${var.image_family_prefix}"
  most_recent       = true
  with_architecture = "x86"
}

locals {
  resolved_image_id = var.image_id != "" ? var.image_id : tostring(data.hcloud_image.api[0].id)
}

# ─────────────────────────── Cloud Servers ───────────────────────────

resource "hcloud_server" "api" {
  count = var.cluster_size

  name        = "${var.prefix}api-${count.index + 1}"
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
    role      = "api"
    pool      = var.node_pool_name
  })

  delete_protection  = !var.allow_force_destroy
  rebuild_protection = !var.allow_force_destroy

  lifecycle {
    ignore_changes = [
      # User-data is baked at first boot; later VMM-changes shouldn't trigger
      # a server-rebuild. Re-bake snapshot in NX.2.6 to roll out updates.
      user_data,
    ]
  }
}
