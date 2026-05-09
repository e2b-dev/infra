/**
 * Hetzner Nodepool — Client (Firecracker-Host)
 *
 * KRITISCH für Manus-Parity. Provisions Hetzner Cloud Server pool that runs
 * the e2b client (Firecracker MicroVM-Host). Manus pattern verified in
 * manus-wiki/manus-4/MISSED_FORENSIK_AUDIT_MANUS4.md M64-M67:
 *   - 6 vCPUs Cascade Lake (Hetzner CCX33 = 8 dedicated CPUs, closest match)
 *   - 3.8 GiB RAM per sandbox (Hetzner CCX33 = 32 GiB → ~7-8 sandboxes/host)
 *   - 41.3 GB ext4 per sandbox (Hetzner CCX33 = 480 GB → enough headroom)
 *   - Boot 430ms via Snapshot-Restore + Warmpool
 *   - HugePages enabled (configurable via base_hugepages_percentage)
 *
 * NOTE: Hetzner Cloud Server vCPUs are virtualized. For TRUE bare-metal
 * (better Firecracker boot times, no nested virt overhead), use Hetzner
 * Robot servers — modeled separately as PRIMARY (NX.2.x post-NX.2.4).
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

  user_data = templatefile("${local.scripts_path}/start-client.sh", {
    NODE_POOL                    = var.node_pool_name
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    CLUSTER_TAG_VALUE            = var.cluster_tag_value
    SCRIPTS_BUCKET               = var.setup_bucket_name
    OBJECT_STORE_URL             = var.object_store_url
    OBJECT_STORE_ACCESS_KEY      = var.object_store_access_key
    OBJECT_STORE_SECRET_KEY      = var.object_store_secret_key
    CONSUL_TOKEN                 = var.consul_acl_token
    CONSUL_GOSSIP_ENCRYPTION_KEY = var.consul_gossip_encryption_key
    NOMAD_TOKEN                  = var.nomad_acl_token

    # Firecracker-Host specifics (1:1 Manus pattern).
    FC_KERNELS_BUCKET         = var.fc_kernels_bucket
    FC_VERSIONS_BUCKET        = var.fc_versions_bucket
    FC_ENV_PIPELINE_BUCKET    = var.fc_env_pipeline_bucket
    FC_BUSYBOX_BUCKET         = var.fc_busybox_bucket
    NODE_LABELS               = join(",", var.node_labels)
    BASE_HUGEPAGES_PERCENTAGE = var.base_hugepages_percentage
    HOSTNAME_SUFFIX           = "${var.prefix}client"
  })
}

# ─────────────────────────── Snapshot Lookup ───────────────────────────

data "hcloud_image" "client" {
  count = var.image_id == "" ? 1 : 0

  with_selector     = "purpose=client,family=${var.image_family_prefix}"
  most_recent       = true
  with_architecture = "x86"
}

locals {
  resolved_image_id = var.image_id != "" ? var.image_id : tostring(data.hcloud_image.client[0].id)
}

# ─────────────────────────── Cloud Servers ───────────────────────────

resource "hcloud_server" "client" {
  count = var.cluster_size

  name        = "${var.prefix}client-${count.index + 1}"
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
    component        = "nodepool"
    role             = "client"
    pool             = var.node_pool_name
    firecracker_host = "true"
  })

  delete_protection  = !var.allow_force_destroy
  rebuild_protection = !var.allow_force_destroy

  lifecycle {
    ignore_changes = [user_data]
  }
}
