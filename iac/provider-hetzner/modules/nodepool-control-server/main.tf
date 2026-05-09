/**
 * Hetzner Nodepool — Control-Server (Nomad/Consul Server)
 *
 * Provisions Nomad+Consul SERVER nodes (not workers). These coordinate the
 * cluster: Raft leader election, ACL management, scheduling decisions.
 * 1:1 with provider-aws/modules/nodepool-control-server.
 *
 * Recommended sizing: 3 servers for HA Raft quorum, 1 for dev/staging.
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

  user_data = templatefile("${local.scripts_path}/start-server.sh", {
    NUM_SERVERS                  = var.cluster_size
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    CLUSTER_TAG_VALUE            = var.cluster_tag_value
    SCRIPTS_BUCKET               = var.setup_bucket_name
    OBJECT_STORE_URL             = var.object_store_url
    OBJECT_STORE_ACCESS_KEY      = var.object_store_access_key
    OBJECT_STORE_SECRET_KEY      = var.object_store_secret_key
    CONSUL_TOKEN                 = var.consul_acl_token
    CONSUL_GOSSIP_ENCRYPTION_KEY = var.consul_gossip_encryption_key
    NOMAD_TOKEN                  = var.nomad_acl_token
    HOSTNAME_SUFFIX              = "${var.prefix}control-server"
  })
}

# ─────────────────────────── Snapshot Lookup ───────────────────────────

data "hcloud_image" "control_server" {
  count = var.image_id == "" ? 1 : 0

  with_selector     = "purpose=control-server,family=${var.image_family_prefix}"
  most_recent       = true
  with_architecture = "x86"
}

locals {
  resolved_image_id = var.image_id != "" ? var.image_id : tostring(data.hcloud_image.control_server[0].id)
}

# ─────────────────────────── Cloud Servers ───────────────────────────

resource "hcloud_server" "control_server" {
  count = var.cluster_size

  name        = "${var.prefix}control-server-${count.index + 1}"
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
    role      = "control-server"
    pool      = "control"
  })

  delete_protection  = !var.allow_force_destroy
  rebuild_protection = !var.allow_force_destroy

  lifecycle {
    ignore_changes = [user_data]
  }
}
