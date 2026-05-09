/**
 * Hetzner Nomad-Cluster Module — Cluster Wiring
 *
 * 1:1 functional with provider-aws/nomad-cluster + provider-gcp/nomad-cluster.
 *
 * Responsibilities:
 *   - Upload run-consul.sh + run-nomad.sh to Hetzner Object Storage (hashed)
 *   - Instantiate the 4 nodepool modules (api/clickhouse/client/control-server)
 *   - Wire cluster-bootstrap inputs from init module
 *   - Compose cluster discovery via Hetzner labels (no AWS Auto-Discovery EC2-tag needed)
 *
 * The actual server provisioning is done by the nodepool-* modules; this
 * module exists to compose them and pass shared cluster bootstrap inputs.
 */

terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.51.0"
    }
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.35.0"
    }
  }
}

# ─────────────────────────── Setup-Files Hash + Upload ───────────────────────────
# Upload the patched run-consul.sh + run-nomad.sh to Hetzner Object Storage.
# Each file is hashed so cloud-init scripts can pin to a specific version.

locals {
  setup_files = {
    "scripts/run-consul.sh" = "run-consul",
    "scripts/run-nomad.sh"  = "run-nomad",
  }

  setup_files_hash = {
    "run-consul" = substr(filesha256("${path.module}/scripts/run-consul.sh"), 0, 8)
    "run-nomad"  = substr(filesha256("${path.module}/scripts/run-nomad.sh"), 0, 8)
  }

  cluster_tag_name  = "cluster-discovery-name"
  cluster_tag_value = "${var.prefix}nomad-cluster"
}

resource "aws_s3_object" "setup_config" {
  for_each = local.setup_files

  bucket = var.setup_bucket_name
  key    = "${each.value}-${local.setup_files_hash[each.value]}.sh"
  source = "${path.module}/${each.key}"
  etag   = filemd5("${path.module}/${each.key}")
}

# ─────────────────────────── Nodepool: Control-Server ───────────────────────────

module "control_server" {
  source = "../modules/nodepool-control-server"

  prefix              = var.prefix
  cluster_size        = var.control_server_cluster_size
  server_type         = var.control_server_type
  location            = var.location
  image_family_prefix = var.snapshot_family_prefix
  ssh_key_ids         = var.ssh_key_ids
  firewall_ids        = var.firewall_ids
  network_id          = var.network_id
  subnet_cidr         = var.cloud_subnet_cidr

  cluster_tag_name             = local.cluster_tag_name
  cluster_tag_value            = local.cluster_tag_value
  setup_bucket_name            = var.setup_bucket_name
  object_store_url             = var.object_store_url
  object_store_access_key      = var.object_store_access_key
  object_store_secret_key      = var.object_store_secret_key
  consul_acl_token             = var.consul_acl_token
  consul_gossip_encryption_key = var.consul_gossip_encryption_key
  nomad_acl_token              = var.nomad_acl_token

  common_labels       = var.common_labels
  allow_force_destroy = var.allow_force_destroy

  depends_on = [aws_s3_object.setup_config]
}

# ─────────────────────────── Nodepool: API ───────────────────────────

module "api" {
  source = "../modules/nodepool-api"

  prefix              = var.prefix
  cluster_size        = var.api_cluster_size
  server_type         = var.api_server_type
  location            = var.location
  image_family_prefix = var.snapshot_family_prefix
  ssh_key_ids         = var.ssh_key_ids
  firewall_ids        = var.firewall_ids
  network_id          = var.network_id
  subnet_cidr         = var.cloud_subnet_cidr

  cluster_tag_name             = local.cluster_tag_name
  cluster_tag_value            = local.cluster_tag_value
  setup_bucket_name            = var.setup_bucket_name
  object_store_url             = var.object_store_url
  object_store_access_key      = var.object_store_access_key
  object_store_secret_key      = var.object_store_secret_key
  consul_acl_token             = var.consul_acl_token
  consul_gossip_encryption_key = var.consul_gossip_encryption_key
  nomad_acl_token              = var.nomad_acl_token
  loki_bucket                  = var.loki_bucket

  common_labels       = var.common_labels
  allow_force_destroy = var.allow_force_destroy

  depends_on = [module.control_server]
}

# ─────────────────────────── Nodepool: ClickHouse ───────────────────────────

module "clickhouse" {
  source = "../modules/nodepool-clickhouse"

  prefix              = var.prefix
  cluster_size        = var.clickhouse_cluster_size
  server_type         = var.clickhouse_server_type
  location            = var.location
  image_family_prefix = var.snapshot_family_prefix
  ssh_key_ids         = var.ssh_key_ids
  firewall_ids        = var.firewall_ids
  network_id          = var.network_id
  subnet_cidr         = var.cloud_subnet_cidr

  cluster_tag_name             = local.cluster_tag_name
  cluster_tag_value            = local.cluster_tag_value
  setup_bucket_name            = var.setup_bucket_name
  clickhouse_backups_bucket    = var.clickhouse_backups_bucket
  object_store_url             = var.object_store_url
  object_store_access_key      = var.object_store_access_key
  object_store_secret_key      = var.object_store_secret_key
  consul_acl_token             = var.consul_acl_token
  consul_gossip_encryption_key = var.consul_gossip_encryption_key
  nomad_acl_token              = var.nomad_acl_token

  common_labels       = var.common_labels
  allow_force_destroy = var.allow_force_destroy

  depends_on = [module.control_server]
}

# ─────────────────────────── Nodepool: Client (Firecracker-Host) ───────────────────────────

module "client" {
  source = "../modules/nodepool-client"

  prefix              = var.prefix
  cluster_size        = var.client_cluster_size
  server_type         = var.client_server_type
  location            = var.location
  image_family_prefix = var.snapshot_family_prefix
  ssh_key_ids         = var.ssh_key_ids
  firewall_ids        = var.firewall_ids
  network_id          = var.network_id
  subnet_cidr         = var.cloud_subnet_cidr

  cluster_tag_name             = local.cluster_tag_name
  cluster_tag_value            = local.cluster_tag_value
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

  common_labels       = var.common_labels
  allow_force_destroy = var.allow_force_destroy

  depends_on = [module.control_server]
}
