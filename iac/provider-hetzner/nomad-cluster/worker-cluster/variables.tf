variable "prefix" {
  type = string
}

variable "cluster_name" {
  type        = string
  description = "Unique name for this worker-cluster (e.g. 'build', 'nbg1-burst')."
}

variable "cluster_size" {
  type    = number
  default = 1

  validation {
    condition     = var.cluster_size >= 1
    error_message = "Cluster size must be at least 1."
  }
}

variable "server_type" {
  type        = string
  description = "Hetzner Cloud Server type. ccx33 for Firecracker-Hosts (8 dedicated CPUs)."
  default     = "ccx33"
}

variable "location" {
  type    = string
  default = "fsn1"
}

variable "image_family_prefix" {
  type = string
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

variable "subnet_cidr" {
  type = string
}

variable "subnet_offset" {
  type        = number
  description = "Subnet offset for IP allocation (must not overlap with primary cluster nodepools)."
  default     = 150
}

# Cluster bootstrap (forwarded from init module)
variable "cluster_tag_name" {
  type = string
}

variable "setup_bucket_name" {
  type = string
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

# Firecracker-host buckets
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

variable "base_hugepages_percentage" {
  type    = number
  default = 80
}

variable "node_labels" {
  type    = list(string)
  default = []
}

variable "common_labels" {
  type    = map(string)
  default = {}
}

variable "allow_force_destroy" {
  type    = bool
  default = false
}
