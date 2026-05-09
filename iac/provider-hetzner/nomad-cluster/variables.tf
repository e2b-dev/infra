variable "prefix" {
  type = string
}

variable "location" {
  type    = string
  default = "fsn1"
}

variable "ssh_key_ids" {
  type    = list(number)
  default = []
}

variable "firewall_ids" {
  type    = list(number)
  default = []
}

variable "network_id" {
  type = number
}

variable "cloud_subnet_cidr" {
  type = string
}

variable "snapshot_family_prefix" {
  type        = string
  description = "Snapshot family prefix produced by Packer-Hetzner (NX.2.6)."
}

# ─────────────────────────── Cluster Bootstrap (from init module) ───────────────────────────

variable "setup_bucket_name" {
  type        = string
  description = "Object Storage bucket where run-consul.sh + run-nomad.sh are uploaded."
}

variable "object_store_url" {
  type = string
}

variable "object_store_access_key" {
  type      = string
  sensitive = true
}

variable "object_store_secret_key" {
  type      = string
  sensitive = true
}

variable "consul_acl_token" {
  type      = string
  sensitive = true
}

variable "consul_gossip_encryption_key" {
  type      = string
  sensitive = true
}

variable "nomad_acl_token" {
  type      = string
  sensitive = true
}

# ─────────────────────────── Nodepool sizing ───────────────────────────

variable "control_server_cluster_size" {
  type    = number
  default = 1
}

variable "control_server_type" {
  type    = string
  default = "cx32"
}

variable "api_cluster_size" {
  type    = number
  default = 1
}

variable "api_server_type" {
  type    = string
  default = "cpx41"
}

variable "clickhouse_cluster_size" {
  type    = number
  default = 1
}

variable "clickhouse_server_type" {
  type    = string
  default = "cpx41"
}

variable "client_cluster_size" {
  type    = number
  default = 1
}

variable "client_server_type" {
  type    = string
  default = "ccx33"
}

variable "base_hugepages_percentage" {
  type    = number
  default = 80
}

# ─────────────────────────── Buckets (NX.2.4 init outputs) ───────────────────────────

variable "loki_bucket" {
  type    = string
  default = ""
}

variable "clickhouse_backups_bucket" {
  type = string
}

variable "fc_kernels_bucket" {
  type = string
}

variable "fc_versions_bucket" {
  type = string
}

variable "fc_env_pipeline_bucket" {
  type = string
}

variable "fc_busybox_bucket" {
  type = string
}

# ─────────────────────────── Common ───────────────────────────

variable "common_labels" {
  type    = map(string)
  default = {}
}

variable "allow_force_destroy" {
  type    = bool
  default = false
}
