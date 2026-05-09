/**
 * Hetzner Worker-Cluster Module
 *
 * Provisions a SECONDARY pool of Firecracker-Host workers in addition to
 * the primary cluster from nomad-cluster/main.tf. Used for:
 *   - Dedicated build-cluster (separate from production-runtime cluster)
 *   - Remote-region cluster (e.g. NBG1 secondary)
 *   - Burst capacity for high-load periods
 *
 * Each worker-cluster gets its own unique cluster_name to namespace
 * Consul service-registry entries and Nomad job-targeting.
 *
 * 1:1 functional with provider-gcp/nomad-cluster/worker-cluster/, but uses
 * Hetzner Cloud Server (no GCE autoscaler — managed via Nomad-autoscaler
 * job in NX.2.7-extended).
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
  cluster_label = "${var.prefix}worker-${var.cluster_name}"
}

# ─────────────────────────── Worker Pool (delegates to nodepool-client) ───────────────────────────

module "worker_pool" {
  source = "../../modules/nodepool-client"

  prefix              = var.prefix
  node_pool_name      = local.cluster_label
  cluster_size        = var.cluster_size
  server_type         = var.server_type
  location            = var.location
  image_family_prefix = var.image_family_prefix
  ssh_key_ids         = var.ssh_key_ids
  firewall_ids        = var.firewall_ids
  network_id          = var.network_id
  subnet_cidr         = var.subnet_cidr
  subnet_offset       = var.subnet_offset

  cluster_tag_name             = var.cluster_tag_name
  cluster_tag_value            = local.cluster_label
  setup_bucket_name            = var.setup_bucket_name
  object_store_url             = var.object_store_url
  object_store_access_key      = var.object_store_access_key
  object_store_secret_key      = var.object_store_secret_key
  consul_acl_token             = var.consul_acl_token
  consul_gossip_encryption_key = var.consul_gossip_encryption_key
  nomad_acl_token              = var.nomad_acl_token

  fc_kernels_bucket         = var.fc_kernels_bucket
  fc_versions_bucket        = var.fc_versions_bucket
  fc_env_pipeline_bucket    = var.fc_env_pipeline_bucket
  fc_busybox_bucket         = var.fc_busybox_bucket
  base_hugepages_percentage = var.base_hugepages_percentage
  node_labels               = concat(var.node_labels, ["worker_cluster=${var.cluster_name}"])

  common_labels = merge(var.common_labels, {
    worker_cluster = var.cluster_name
  })
  allow_force_destroy = var.allow_force_destroy
}
