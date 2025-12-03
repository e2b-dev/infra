variable "client_cluster_config" {
  description = "Client cluster configuration object containing all cluster settings"
  type = object({
    size                      = number
    size_max                  = number
    autoscaling_cpu_target    = number
    autoscaling_memory_target = number
    machine_type              = string
    min_cpu_platform          = string
    cache_disk_size_gb        = number
    cache_disk_type           = string
    cache_disk_count          = number
    boot_disk_type            = string
  })

  validation {
    condition     = var.client_cluster_config.cache_disk_count == 1 && var.client_cluster_config.cache_disk_type != "local-ssd" || var.client_cluster_config.cache_disk_count > 0 && var.client_cluster_config.cache_disk_type == "local-ssd"
    error_message = "If cache_disk_type is 'local-ssd', cache_disk_count must be greater than 0. If cache_disk_type is not 'local-ssd', cache_disk_count must be 1."
  }

  validation {
    condition     = var.client_cluster_config.autoscaling_cpu_target >= 0 && var.client_cluster_config.autoscaling_cpu_target <= 1
    error_message = "autoscaling_cpu_target must be between 0 and 1."
  }

  validation {
    condition     = var.client_cluster_config.autoscaling_memory_target >= 0 && var.client_cluster_config.autoscaling_memory_target <= 100
    error_message = "autoscaling_memory_target must be between 0 and 100."
  }
}

variable "client_cluster_name" {
  description = "Name of the client cluster"
  type        = string
}

variable "client_image_family" {
  description = "GCE image family for the client instances"
  type        = string
}

# GCP CONFIGURATION

variable "gcp_region" {
  description = "GCP region where the client cluster will be deployed"
  type        = string
}

variable "gcp_zone" {
  description = "GCP zone where the client cluster will be deployed"
  type        = string
}

variable "network_name" {
  description = "Name of the VPC network for the client cluster"
  type        = string
}

variable "cluster_tag_name" {
  description = "Network tag applied to client cluster instances for firewall rules"
  type        = string
}

# SERVICE ACCOUNT & AUTHENTICATION

variable "google_service_account_email" {
  description = "Email of the Google service account used by client instances"
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

variable "orchestrator_node_pool" {
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

variable "orchestrator_base_hugepages_percentage" {
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

variable "prefix" {
  description = "Prefix for resource naming"
  type        = string
}

variable "labels" {
  description = "Labels to apply to all resources"
  type        = map(string)
}

variable "file_hash" {
  description = "Map of setup script file paths to their content hashes for versioning"
  type        = map(string)
}
