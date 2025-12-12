variable "autoscaler" {
  type = object({
    size_min      = number
    size_max      = optional(number)
    cpu_target    = optional(number)
    memory_target = optional(number)
  })

  validation {
    condition     = var.autoscaler.size_max >= var.autoscaler.size_min
    error_message = "autoscaling_size_max must be >= autoscaling_size_min."
  }

  validation {
    condition     = var.autoscaler.cpu_target >= 0 && var.autoscaler.cpu_target <= 1
    error_message = "autoscaling_cpu_target must be between 0 and 1."
  }

  validation {
    condition     = var.autoscaler.memory_target >= 0 && var.autoscaler.memory_target <= 100
    error_message = "autoscaling_memory_target must be between 0 and 100."
  }
}

variable "machine_type" {
  type = string
}

variable "min_cpu_platform" {
  type = string
}

variable "boot_disk" {
  type = object({
    disk_type = string
    size_gb   = number
  })
}

variable "cache_disks" {
  type = object({
    disk_type = string
    size_gb   = number
    count     = number
  })

  validation {
    condition     = var.cache_disks.count >= 1
    error_message = "You have to have at least one cache disk"
  }
}

variable "cluster_name" {
  description = "Name of the cluster"
  type        = string
}

variable "image_family" {
  description = "GCE image family for the instances"
  type        = string
}

# GCP CONFIGURATION

variable "gcp_region" {
  description = "GCP region where the cluster will be deployed"
  type        = string
}

variable "gcp_zone" {
  description = "GCP zone where the cluster will be deployed"
  type        = string
}

variable "network_name" {
  description = "Name of the VPC network for the cluster"
  type        = string
}

variable "cluster_tag_name" {
  description = "Network tag applied to cluster instances for firewall rules"
  type        = string
}

# SERVICE ACCOUNT & AUTHENTICATION

variable "google_service_account_email" {
  description = "Email of the Google service account used by instances"
  type        = string
}

variable "google_service_account_key" {
  description = "JSON key for the Google service account"
  type        = string
  sensitive   = true
}

# ---------------------------------------------------------------------------------------------------------------------
# NOMAD & CONSUL CONFIGURATION
# ---------------------------------------------------------------------------------------------------------------------

variable "nomad_port" {
  description = "Port number for Nomad server communication"
  type        = number
}

variable "nomad_acl_token_secret" {
  description = "Nomad ACL token for client authentication"
  type        = string
  sensitive   = true
}

variable "consul_acl_token_secret" {
  description = "Consul ACL token for client authentication"
  type        = string
  sensitive   = true
}

variable "consul_gossip_encryption_key_secret_data" {
  description = "Consul gossip encryption key from secret manager"
  type        = string
  sensitive   = true
}

variable "consul_dns_request_token_secret_data" {
  description = "Consul DNS request token from secret manager"
  type        = string
  sensitive   = true
}

variable "node_pool" {
  description = "Nomad node pool for orchestrator workloads"
  type        = string
}

# STORAGE BUCKETS

variable "docker_contexts_bucket_name" {
  description = "GCS bucket name for Docker build contexts"
  type        = string
}

variable "cluster_setup_bucket_name" {
  description = "GCS bucket name for cluster setup scripts and configuration"
  type        = string
}

variable "fc_env_pipeline_bucket_name" {
  description = "GCS bucket name for Firecracker environment pipeline"
  type        = string
}

variable "fc_kernels_bucket_name" {
  description = "GCS bucket name for Firecracker kernels"
  type        = string
}

variable "fc_versions_bucket_name" {
  description = "GCS bucket name for Firecracker versions"
  type        = string
}

# NFS CONFIGURATION

variable "filestore_cache_enabled" {
  description = "Whether Filestore-based shared cache is enabled"
  type        = bool
}

variable "nfs_ip_addresses" {
  description = "NFS IP addresses from filestore module (empty if filestore is disabled)"
  type        = list(string)
}

variable "nfs_mount_path" {
  description = "Mount path for NFS shared storage"
  type        = string
}

variable "nfs_mount_subdir" {
  description = "Subdirectory within NFS mount for cache storage"
  type        = string
}

variable "nfs_mount_opts" {
  description = "NFS mount options for performance tuning"
  type        = string
}

# ORCHESTRATOR CONFIGURATION

variable "base_hugepages_percentage" {
  description = "Percentage of memory to allocate for hugepages in orchestrator"
  type        = number
}

# DEPLOYMENT METADATA

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "Environment must be one of: dev, staging, prod"
  }
}

variable "labels" {
  description = "Labels to apply to all resources"
  type        = map(string)
}

variable "file_hash" {
  description = "Map of setup script file paths to their content hashes for versioning"
  type        = map(string)
}
