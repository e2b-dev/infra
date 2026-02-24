variable "cluster_name" {
  description = "Name of the worker cluster"
  type        = string
}

variable "cluster_size" {
  type = number

  validation {
    condition     = var.cluster_size >= 1
    error_message = "Cluster size must be at least 1."
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
  description = "EC2 instance type. Must be .metal for Firecracker KVM support."
  type        = string
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
