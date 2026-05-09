/**
 * Provider-Hetzner — Variables
 *
 * All Hetzner-specific inputs for the e2b infrastructure.
 * EU-sovereign defaults: FSN1 (Falkenstein), eu-central network zone.
 */

# ─────────────────────────── Hetzner Cloud Authentication ───────────────────────────

variable "hetzner_api_token" {
  type        = string
  description = "Hetzner Cloud API token (read+write). Generated in Hetzner Console → Security → API Tokens."
  sensitive   = true
}

variable "hetzner_dns_token" {
  type        = string
  description = "Hetzner DNS API token (separate from Cloud token). Generated in Hetzner DNS Console."
  sensitive   = true
  default     = ""
}

variable "hetzner_robot_user" {
  type        = string
  description = "Hetzner Robot user (for bare-metal Robot servers like PRIMARY). Format: #ws+username."
  sensitive   = true
  default     = ""
}

variable "hetzner_robot_password" {
  type        = string
  description = "Hetzner Robot password (paired with hetzner_robot_user)."
  sensitive   = true
  default     = ""
}

variable "hetzner_ssh_key_ids" {
  type        = list(number)
  description = "List of pre-existing Hetzner Cloud SSH key IDs to attach to all provisioned servers. Empty list = no key attached (rare)."
  default     = []
}

# ─────────────────────────── Hetzner Object Storage (S3-Compatible) ───────────────────────────

variable "hetzner_object_storage_region" {
  type        = string
  description = "Hetzner Object Storage region. One of: fsn1, nbg1, hel1. All EU-sovereign."
  default     = "fsn1"
  validation {
    condition     = contains(["fsn1", "nbg1", "hel1"], var.hetzner_object_storage_region)
    error_message = "hetzner_object_storage_region must be one of fsn1 (Falkenstein), nbg1 (Nuremberg), hel1 (Helsinki)."
  }
}

variable "hetzner_object_storage_access_key" {
  type        = string
  description = "Hetzner Object Storage S3-compatible access key. Generated in Hetzner Console → Object Storage → Credentials."
  sensitive   = true
}

variable "hetzner_object_storage_secret_key" {
  type        = string
  description = "Hetzner Object Storage S3-compatible secret key (paired with access_key)."
  sensitive   = true
}

# ─────────────────────────── Hetzner Region + Datacenter ───────────────────────────

variable "hetzner_region" {
  type        = string
  description = "Hetzner region for cloud resources. Examples: fsn1 (Falkenstein/DE), nbg1 (Nuremberg/DE), hel1 (Helsinki/FI)."
  default     = "fsn1"
}

variable "hetzner_network_zone" {
  type        = string
  description = "Hetzner network zone for Cloud Networks. Must contain hetzner_region."
  default     = "eu-central"
  validation {
    condition     = contains(["eu-central", "us-east", "us-west", "ap-southeast"], var.hetzner_network_zone)
    error_message = "hetzner_network_zone must be one of eu-central, us-east, us-west, ap-southeast."
  }
}

variable "hetzner_datacenter" {
  type        = string
  description = "Hetzner datacenter for primary placement (e.g. fsn1-dc14). Used as default for compute resources."
  default     = "fsn1-dc14"
}

# ─────────────────────────── Hetzner vSwitch (L2 Cloud↔Robot) ───────────────────────────

variable "hetzner_vswitch_id" {
  type        = number
  description = "Existing Hetzner vSwitch ID for Cloud↔Robot bridging. 0 = no vSwitch (skip Robot integration)."
  default     = 0
}

variable "hetzner_vlan_id" {
  type        = number
  description = "VLAN ID for the vSwitch (Hetzner range 4000-4091). Default 4000 matches existing MaxiCore production."
  default     = 4000
}

# ─────────────────────────── DNS Strategy ───────────────────────────

variable "use_cloudflare_dns" {
  type        = bool
  description = "If true, use Cloudflare for DNS records (legacy/migration path). If false, use Hetzner DNS."
  default     = false
}

variable "cloudflare_api_token" {
  type        = string
  description = "Cloudflare API token (only required when use_cloudflare_dns = true)."
  sensitive   = true
  default     = ""
}

# ─────────────────────────── E2B Common Variables ───────────────────────────

variable "domain_name" {
  type        = string
  description = "Primary domain or subdomain for the E2B deployment (e.g. sandbox.helix12.eu)."
}

variable "prefix" {
  type        = string
  description = "Name prefix for ALL provisioned resources. Must end with '-'. Example: 'maxi-' or 'helix-'."
  default     = "maxicore-"
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]*-$", var.prefix))
    error_message = "prefix must be lowercase alphanumeric+dash and end with a dash."
  }
}

variable "bucket_prefix" {
  type        = string
  description = "Object Storage bucket name prefix. Empty = derived from prefix."
  default     = ""
}

variable "environment" {
  type        = string
  description = "Deployment environment. One of: dev, staging, prod."
  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be one of dev, staging, prod."
  }
}

variable "allow_force_destroy" {
  type        = bool
  description = "If true, all created resources can be force-destroyed even if non-empty. Should be false in prod."
  default     = false
}

# ─────────────────────────── Network CIDRs ───────────────────────────

variable "cloud_network_cidr" {
  type        = string
  description = "CIDR range for the Hetzner Cloud Network. Default 10.0.0.0/8 matches MaxiCore production."
  default     = "10.0.0.0/8"
}

variable "cloud_subnet_cidr" {
  type        = string
  description = "CIDR range for the Cloud Subnet (within cloud_network_cidr). Default 10.0.1.0/24."
  default     = "10.0.1.0/24"
}

variable "vswitch_subnet_cidr" {
  type        = string
  description = "CIDR range for the vSwitch subnet (Cloud↔Robot bridge). Default 10.10.0.0/24."
  default     = "10.10.0.0/24"
}

# ─────────────────────────── Cluster Sizing (forwarded to nomad-cluster) ───────────────────────────

variable "redis_managed" {
  type        = bool
  description = "If true, provision a separate Cloud Server running Redis (Hetzner has no managed Redis). If false, run Redis as a Nomad job inside the cluster."
  default     = false
}

variable "redis_server_type" {
  type        = string
  description = "Hetzner Cloud Server type for Redis. Default cx22 (2vCPU/4GB)."
  default     = "cx22"
}

variable "redis_replica_size" {
  type        = number
  description = "Number of Redis replicas (for HA). 0 = single node."
  default     = 0
}

variable "api_cluster_size" {
  type        = number
  description = "Number of API server nodes."
  default     = 1
}

variable "api_internal_grpc_port" {
  type        = number
  description = "Internal gRPC port for the API service (orchestrator-side)."
  default     = 5009
}

variable "api_server_type" {
  type        = string
  description = "Hetzner Cloud Server type for API nodes. Default cpx41 (8vCPU/16GB) — matches AWS t3.xlarge."
  default     = "cpx41"
}

variable "api_image_family_prefix" {
  type        = string
  description = "Hetzner Cloud Snapshot family prefix for API server images (built by Packer)."
  default     = ""
}

variable "ingress_count" {
  type        = number
  description = "Number of ingress (Cloud LB) instances. Hetzner Cloud LB is regional, so usually 1."
  default     = 1
}

variable "client_proxy_count" {
  type        = number
  description = "Number of client-proxy nodes (front-facing reverse proxy)."
  default     = 1
}

variable "clickhouse_cluster_size" {
  type        = number
  description = "Number of ClickHouse nodes."
  default     = 1
}

variable "clickhouse_server_type" {
  type        = string
  description = "Hetzner Cloud Server type for ClickHouse. Default cpx41 (8vCPU/16GB) — matches AWS t3.xlarge."
  default     = "cpx41"
}

variable "clickhouse_image_family_prefix" {
  type        = string
  description = "Hetzner Cloud Snapshot family prefix for ClickHouse images."
  default     = ""
}

# ─────────────────────────── Build/Client Pools ───────────────────────────

variable "build_clusters_config" {
  type        = string
  description = "JSON string defining build cluster configs. See .env.hetzner.template for examples."
  default     = "{}"
}

variable "client_clusters_config" {
  type        = string
  description = "JSON string defining client (Firecracker-host) cluster configs. See .env.hetzner.template for examples."
  default     = "{}"
}

# ─────────────────────────── Robot Server Integration (PRIMARY) ───────────────────────────

variable "primary_robot_id" {
  type        = number
  description = "Hetzner Robot server ID for the primary VMM-host (data source only, not provisioned). 0 = no Robot adoption."
  default     = 0
}

variable "primary_robot_ip" {
  type        = string
  description = "Hetzner Robot server public IP (for SSH provisioning of bare-metal services)."
  default     = ""
}

# ─────────────────────────── Sandbox Firewall (Egress Allowlist) ───────────────────────────

variable "allow_sandbox_internal_cidrs" {
  type        = list(string)
  description = "CIDRs that sandboxes are allowed to reach in private ranges. Comma-separated string in env, list here."
  default     = []
}

variable "additional_api_paths_handled_by_ingress" {
  type        = list(any)
  description = "Additional API paths handled by the ingress LB (legacy + new format). Forwarded to ingress module."
  default     = []
}
