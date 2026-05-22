variable "gcp_region" {
  type = string
}

variable "gcp_zone" {
  description = "GCP zone used for labels (ops agent policy). Does not restrict MIG placement."
  type        = string
}

variable "network_name" {
  type = string
}

variable "cluster_tag_name" {
  description = "Network tag applied to cluster instances for firewall rules and Consul auto-discovery."
  type        = string
}

variable "cluster_name" {
  description = "Name of the cluster (used as base_instance_name and resource name prefix)."
  type        = string
}

variable "cluster_size" {
  type = number

  validation {
    condition     = var.cluster_size >= 1
    error_message = "Cluster size must be at least 1."
  }
}

variable "machine_type" {
  type = string
}

variable "image_family" {
  description = "GCE image family for the API instances."
  type        = string
}

variable "boot_disk_type" {
  description = "GCE boot disk type for the API instances."
  type        = string
}

variable "api_use_nat" {
  description = "Whether API nodes should route outbound traffic through NAT (no external IPs)."
  type        = bool
}

# ---------------------------------------------------------------------------------------------------------------------
# LOAD BALANCER NAMED PORTS
# ---------------------------------------------------------------------------------------------------------------------

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "client_proxy_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
}

variable "client_proxy_port" {
  type = object({
    name = string
    port = number
  })
}

variable "ingress_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

# ---------------------------------------------------------------------------------------------------------------------
# SERVICE ACCOUNT & AUTHENTICATION
# ---------------------------------------------------------------------------------------------------------------------

variable "google_service_account_email" {
  type = string
}

variable "google_service_account_key" {
  type      = string
  sensitive = true
}

# ---------------------------------------------------------------------------------------------------------------------
# NOMAD & CONSUL CONFIGURATION
# ---------------------------------------------------------------------------------------------------------------------

variable "nomad_port" {
  type = number
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
  description = "Nomad node pool name for API workloads."
  type        = string
}

# ---------------------------------------------------------------------------------------------------------------------
# STORAGE BUCKETS
# ---------------------------------------------------------------------------------------------------------------------

variable "cluster_setup_bucket_name" {
  description = "GCS bucket containing the run-nomad.sh / run-consul.sh setup scripts."
  type        = string
}

# ---------------------------------------------------------------------------------------------------------------------
# DEPLOYMENT METADATA
# ---------------------------------------------------------------------------------------------------------------------

variable "environment" {
  type = string
  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "Environment must be one of: dev, staging, prod"
  }
}

variable "labels" {
  type = map(string)
}

variable "file_hash" {
  description = "Map of setup script file paths to their content hashes for versioning."
  type        = map(string)
}
