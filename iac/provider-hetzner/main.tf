/**
 * Provider-Hetzner — E2B Infrastructure on Hetzner Cloud + Robot
 *
 * Adapts e2b-dev/infra to run on Hetzner Cloud (FSN1/NBG1/HEL1) with
 * Hetzner-Native-First strategy: Cloud Server, Cloud Network, vSwitch,
 * Cloud LB, Cloud Firewall, Cloud Snapshots, Object Storage, DNS — all
 * native Hetzner services where possible. EU-sovereignty by design.
 *
 * Manus 1:1 pattern preserved: Firecracker MicroVMs, e2b orchestrator,
 * Nomad service mesh, ClickHouse analytics, Loki logs, OTEL collector.
 *
 * Architecture-Decision: ADR-0027 (helix12-maxicore-platform/docs/adr/).
 */

terraform {
  required_version = ">= 1.5.0, < 1.16.0"

  # Hetzner Object Storage as S3-compatible backend (EU-sovereign, no AWS).
  # Endpoint pattern: https://{region}.your-objectstorage.com
  # Authentication via S3-style access_key/secret_key from Hetzner Console.
  backend "s3" {
    key                         = "terraform/orchestration/state"
    skip_credentials_validation = true
    skip_metadata_api_check     = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_s3_checksum            = true
    use_path_style              = true
    # endpoint, region, bucket are set via terraform init -backend-config=...
  }

  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.51.0"
    }

    hetznerdns = {
      source  = "germanbrew/hetznerdns"
      version = "~> 3.0"
    }

    # Cloudflare retained for legacy DNS zones during migration.
    # New zones should use hetznerdns. Hybrid is supported.
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.52.5"
    }

    # Nomad provider — same as AWS/GCP variants (provider-agnostic mesh).
    nomad = {
      source  = "hashicorp/nomad"
      version = "2.1.0"
    }

    # AWS provider used ONLY for S3-compatible Hetzner Object Storage.
    # No actual AWS resources are provisioned through this provider.
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.35.0"
    }

    random = {
      source  = "hashicorp/random"
      version = "~> 3.8"
    }

    null = {
      source  = "hashicorp/null"
      version = "~> 3.2"
    }

    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}

# ─────────────────────────── Provider Configuration ───────────────────────────

provider "hcloud" {
  token = var.hetzner_api_token
}

provider "hetznerdns" {
  api_token = var.hetzner_dns_token
}

provider "cloudflare" {
  api_token = var.cloudflare_api_token
}

# AWS provider configured for Hetzner Object Storage S3-compatible API.
# All resources using this provider must be S3-compatible (bucket, object).
provider "aws" {
  region                      = var.hetzner_object_storage_region
  access_key                  = var.hetzner_object_storage_access_key
  secret_key                  = var.hetzner_object_storage_secret_key
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_region_validation      = true
  skip_requesting_account_id  = true

  endpoints {
    s3 = "https://${var.hetzner_object_storage_region}.your-objectstorage.com"
  }
}

provider "nomad" {
  address      = "https://nomad.${var.domain_name}"
  secret_id    = module.init.cluster.nomad_acl_token
  consul_token = module.init.cluster.consul_acl_token
}

# ─────────────────────────── Common Locals ───────────────────────────

locals {
  redis_port   = 6379
  ingress_port = 8080
  nomad_port   = 4646

  api_pool_name          = "api"
  client_pool_name       = "default"
  build_pool_name        = "build"
  clickhouse_pool_name   = "clickhouse"
  clickhouse_jobs_prefix = "clickhouse"

  # Hetzner Cloud Snapshot name prefix matching what Packer-Hetzner produces.
  snapshot_family_prefix = "${var.prefix}orch-"

  # Object Storage bucket prefix (Hetzner Object Storage namespacing).
  bucket_prefix = var.bucket_prefix != "" ? var.bucket_prefix : "${var.prefix}orch"

  # Common labels applied to every Hetzner resource for cost-tracking and FinOps.
  common_labels = {
    project     = "maxicore"
    provider    = "hetzner"
    environment = var.environment
    prefix      = trimsuffix(var.prefix, "-")
    managed_by  = "terraform"
  }

  # Region/datacenter pinning (EU-sovereign).
  region           = var.hetzner_region
  primary_dc       = var.hetzner_datacenter
  network_zone     = var.hetzner_network_zone
  object_store_url = "https://${var.hetzner_object_storage_region}.your-objectstorage.com"
}

# ─────────────────────────── Data Sources ───────────────────────────

data "hcloud_locations" "available" {}

data "hcloud_datacenters" "available" {}

data "hetznerdns_zone" "domain" {
  name = local.domain_root
}

# Compute root domain (e.g. helix12.eu from sandbox.helix12.eu).
locals {
  domain_parts        = split(".", var.domain_name)
  domain_is_subdomain = length(local.domain_parts) > 2
  domain_root         = local.domain_is_subdomain ? join(".", slice(local.domain_parts, length(local.domain_parts) - 2, length(local.domain_parts))) : var.domain_name
}

# ─────────────────────────── Init Module ───────────────────────────

module "init" {
  source = "./init"

  prefix           = var.prefix
  bucket_prefix    = local.bucket_prefix
  environment      = var.environment
  region           = local.region
  network_zone     = local.network_zone
  domain_name      = var.domain_name
  domain_root      = local.domain_root
  common_labels    = local.common_labels
  object_store_url = local.object_store_url

  hetzner_api_token = var.hetzner_api_token
  ssh_key_ids       = var.hetzner_ssh_key_ids

  allow_force_destroy = var.allow_force_destroy
}

# ─────────────────────────── Network Module ───────────────────────────

module "network" {
  source = "./modules/network"

  prefix            = var.prefix
  network_zone      = local.network_zone
  cloud_cidr        = var.cloud_network_cidr
  cloud_subnet_cidr = var.cloud_subnet_cidr
  vswitch_cidr      = var.vswitch_subnet_cidr
  vswitch_id        = var.hetzner_vswitch_id
  vlan_id           = var.hetzner_vlan_id
  common_labels     = local.common_labels
}

# ─────────────────────────── Cloudflare Compatibility Module ───────────────────────────
#
# For users still using Cloudflare DNS (legacy migration path).
# Hetzner DNS is the preferred path; Cloudflare is opt-in via var.use_cloudflare_dns.

module "cloudflare" {
  source = "./modules/cloudflare"
  count  = var.use_cloudflare_dns ? 1 : 0

  domain_root = local.domain_root
  domain_name = var.domain_name

  cloudflare_api_token = var.cloudflare_api_token
}

# ─────────────────────────── Output: Cluster Bootstrap Info ───────────────────────────

output "cluster_bootstrap" {
  description = "Bootstrap info passed to subsequent NX.2.x sprints (network, compute, etc.)"
  sensitive   = true
  value = {
    network_id         = module.network.cloud_network_id
    cloud_subnet_id    = module.network.cloud_subnet_id
    vswitch_subnet_id  = module.network.vswitch_subnet_id
    bucket_prefix      = local.bucket_prefix
    snapshot_prefix    = local.snapshot_family_prefix
    object_store_url   = local.object_store_url
    domain_name        = var.domain_name
    domain_root        = local.domain_root
    region             = local.region
    network_zone       = local.network_zone
    primary_datacenter = local.primary_dc
  }
}

output "init" {
  description = "Init module outputs (buckets, secrets, ACL tokens)."
  sensitive   = true
  value       = module.init
}
