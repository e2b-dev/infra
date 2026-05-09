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
 *
 * ═══════════════════════════════════════════════════════════════════
 * IMPORTANT — WHY `hashicorp/aws` PROVIDER IN A 100% HETZNER STACK?
 * ═══════════════════════════════════════════════════════════════════
 *
 *   The `aws` provider declared below is used EXCLUSIVELY as a generic
 *   S3-API client to talk to Hetzner Object Storage (the native Hetzner
 *   service for object storage, available in FSN1/NBG1/HEL1).
 *
 *   - NO AWS account is used or required.
 *   - NO AWS resources are provisioned.
 *   - NO data ever flows to AWS — every byte stays on Hetzner DE/FI.
 *   - The `endpoints { s3 = "https://{region}.your-objectstorage.com" }`
 *     redirects all S3-API calls to Hetzner's object storage endpoint.
 *
 *   This is the standard Terraform pattern for any S3-compatible storage
 *   (Hetzner Object Storage, Wasabi, Backblaze B2, MinIO, Cloudflare R2,
 *   Scaleway, …) because Hashicorp does not maintain dedicated providers
 *   for each S3-compatible vendor — they all reuse `hashicorp/aws` with
 *   a custom endpoint.
 *
 *   Hetzner does NOT publish a dedicated terraform-provider for Object
 *   Storage. Using the AWS provider as an S3-client is the recommended
 *   path documented in https://docs.hetzner.com/storage/object-storage/.
 *
 *   Verification:
 *   - The `provider "aws"` block has `endpoints.s3` set to the Hetzner URL.
 *   - The Terraform backend `backend "s3"` is similarly configured to
 *     write state to Hetzner Object Storage, not AWS S3.
 *   - All buckets created via `aws_s3_bucket` exist on Hetzner Object
 *     Storage (visible in the Hetzner Console under Object Storage).
 *
 *   This satisfies §203 StGB and GDPR EU-sovereignty requirements
 *   without exception.
 * ═══════════════════════════════════════════════════════════════════
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

    # ACME provider for Let's Encrypt wildcard cert (DNS-01 via Hetzner DNS).
    acme = {
      source  = "vancluever/acme"
      version = "~> 2.21"
    }
  }
}

# ─────────────────────────── ACME Provider ───────────────────────────

provider "acme" {
  server_url = var.acme_server_url
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

# ─────────────────────────── DNS Module (Hetzner DNS, primary) ───────────────────────────

module "dns" {
  source = "./modules/dns"
  count  = var.use_cloudflare_dns ? 0 : 1

  domain_root          = local.domain_root
  domain_name          = var.domain_name
  lb_ipv4              = var.lb_ipv4
  lb_ipv6              = var.lb_ipv6
  lb_hostname          = var.lb_hostname
  create_apex_a_record = var.create_apex_a_record
  additional_records   = var.additional_dns_records
}

# ─────────────────────────── Cert Module (Let's Encrypt Wildcard via DNS-01) ───────────────────────────

module "cert" {
  source = "./modules/cert"
  count  = var.enable_lets_encrypt && !var.use_cloudflare_dns ? 1 : 0

  prefix            = var.prefix
  domain_name       = var.domain_name
  acme_email        = var.acme_email
  hetzner_dns_token = var.hetzner_dns_token
  additional_sans   = var.cert_additional_sans
  upload_to_hcloud  = var.cert_upload_to_hcloud
  common_labels     = local.common_labels
}

# ─────────────────────────── Cloud Load Balancer Module (NX.2.4) ───────────────────────────

module "cloud_lb" {
  source = "./modules/cloud-lb"
  count  = var.enable_cloud_lb && var.enable_lets_encrypt ? 1 : 0

  prefix                = var.prefix
  lb_type               = var.lb_type
  location              = local.region
  algorithm             = var.lb_algorithm
  network_id            = module.network.cloud_network_id
  subnet_cidr           = var.cloud_subnet_cidr
  certificate_id        = module.cert[0].hcloud_certificate_id
  ingress_port          = local.ingress_port
  enable_grpc           = var.lb_enable_grpc
  enable_nomad_listener = var.lb_enable_nomad_listener
  common_labels         = local.common_labels
  allow_force_destroy   = var.allow_force_destroy

  depends_on = [module.network, module.cert]
}

# ─────────────────────────── Redis Module (NX.2.4) ───────────────────────────

module "redis" {
  source = "./modules/redis"
  count  = var.redis_managed ? 1 : 0

  prefix              = var.prefix
  server_type         = var.redis_server_type
  location            = local.region
  network_id          = module.network.cloud_network_id
  subnet_cidr         = var.cloud_subnet_cidr
  port                = local.redis_port
  auth_token          = var.redis_auth_token != "" ? var.redis_auth_token : random_password.redis_auth.result
  replica_size        = var.redis_replica_size
  data_volume_size_gb = var.redis_data_volume_size_gb
  common_labels       = local.common_labels
  allow_force_destroy = var.allow_force_destroy

  depends_on = [module.network]
}

resource "random_password" "redis_auth" {
  length  = 32
  special = false
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

output "dns" {
  description = "DNS module outputs (zone id, NS, wildcard record). null when use_cloudflare_dns=true."
  value = {
    zone_id               = try(module.dns[0].zone_id, null)
    zone_name             = try(module.dns[0].zone_name, null)
    zone_ns               = try(module.dns[0].zone_ns, null)
    wildcard_record       = try(module.dns[0].wildcard_record, null)
    manus_pattern_records = try(module.dns[0].manus_pattern_records, {})
    vps_wildcard_record   = try(module.dns[0].vps_wildcard_record, null)
  }
}

output "cert" {
  description = "Cert module outputs (cert PEM, hcloud cert id, expiry). null when enable_lets_encrypt=false."
  sensitive   = true
  value = {
    common_name               = try(module.cert[0].common_name, null)
    subject_alternative_names = try(module.cert[0].subject_alternative_names, null)
    hcloud_certificate_id     = try(module.cert[0].hcloud_certificate_id, null)
    hcloud_certificate_name   = try(module.cert[0].hcloud_certificate_name, null)
    not_after                 = try(module.cert[0].not_after, null)
  }
}

output "cloud_lb" {
  description = "Cloud LB outputs (NX.2.4). null when enable_cloud_lb=false."
  value = {
    lb_id           = try(module.cloud_lb[0].lb_id, null)
    lb_name         = try(module.cloud_lb[0].lb_name, null)
    lb_ipv4         = try(module.cloud_lb[0].lb_ipv4, null)
    lb_ipv6         = try(module.cloud_lb[0].lb_ipv6, null)
    lb_private_ipv4 = try(module.cloud_lb[0].lb_private_ipv4, null)
    lb_hostname     = try(module.cloud_lb[0].lb_hostname, null)
  }
}

output "redis" {
  description = "Redis outputs (NX.2.4). null when redis_managed=false."
  sensitive   = true
  value = {
    primary_endpoint = try(module.redis[0].primary_endpoint, null)
    replica_ips      = try(module.redis[0].replica_ips, [])
    redis_url        = try(module.redis[0].redis_url, null)
  }
}
