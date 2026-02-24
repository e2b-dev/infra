variable "cluster_name" {
  description = "Name of the worker cluster"
  type        = string
}

variable "cluster_size" {
  type = number

  validation {
    condition     = var.cluster_size >= 0
    error_message = "Cluster size must be at least 0."
  }
}

variable "autoscaler" {
  type = object({
    max_size   = optional(number)
    cpu_target = optional(number)
  })
  default = null
}

variable "instance_type" {
  description = "EC2 instance type. Use .metal for bare-metal KVM, or C8i/M8i/R8i with nested_virtualization=true."
  type        = string
}

variable "nested_virtualization" {
  description = "Enable nested virtualization (required for Firecracker on non-metal instances like C8i/M8i/R8i)"
  type        = bool
  default     = false
}

variable "cache_disk_size_gb" {
  description = "Size of EBS cache disk in GB. Set to 0 to skip (when using NVMe instance store)."
  type        = number
  default     = 0
}

variable "ami_id" {
  description = "AMI ID for worker instances"
  type        = string
}

variable "boot_disk_size_gb" {
  type    = number
  default = 100
}

variable "boot_disk_type" {
  type    = string
  default = "gp3"
}

variable "aws_region" {
  type = string
}

variable "availability_zones" {
  type = list(string)
}

variable "subnet_ids" {
  type = list(string)
}

variable "security_group_ids" {
  type = list(string)
}

variable "iam_instance_profile_name" {
  type = string
}

# Nomad & Consul
variable "nomad_port" {
  type    = number
  default = 4646
}

variable "nomad_acl_token_secret" {
  type      = string
  sensitive = true
}

variable "consul_acl_token_secret" {
  type      = string
  sensitive = true
}

variable "consul_gossip_encryption_key_secret_data" {
  type      = string
  sensitive = true
}

variable "consul_dns_request_token_secret_data" {
  type      = string
  sensitive = true
}

variable "node_pool" {
  type = string
}

# Storage buckets
variable "cluster_setup_bucket_name" {
  type = string
}

variable "docker_contexts_bucket_name" {
  type = string
}

variable "fc_env_pipeline_bucket_name" {
  type = string
}

variable "fc_kernels_bucket_name" {
  type = string
}

variable "fc_versions_bucket_name" {
  type = string
}

# EFS
variable "efs_cache_enabled" {
  type    = bool
  default = false
}

variable "efs_dns_name" {
  type    = string
  default = ""
}

variable "efs_mount_path" {
  type    = string
  default = "/orchestrator/shared-store"
}

variable "efs_mount_subdir" {
  type    = string
  default = "chunks-cache"
}

variable "hugepages_percentage" {
  type    = number
  default = 80
}

variable "environment" {
  type = string
}

variable "prefix" {
  type = string
}

variable "tags" {
  type = map(string)
}

variable "file_hash" {
  type = map(string)
}
