variable "prefix" {
  type = string
}

variable "environment" {
  description = "The environment (e.g. staging, prod)."
  type        = string
}

variable "cluster_tag_name" {
  description = "The tag name the Compute Instances will look for to automatically discover each other and form a cluster. TIP: If running more than one server Server cluster, each cluster should have its own unique tag name."
  type        = string
  default     = "orch"
}

variable "server_image_family" {
  type    = string
  default = "e2b-orch"
}

variable "server_cluster_name" {
  type    = string
  default = "orch-server"
}

variable "server_cluster_size" {
  type = number
}

variable "server_machine_type" {
  type = string
}

variable "api_image_family" {
  type    = string
  default = "e2b-orch"
}

variable "api_cluster_size" {
  type = number
}

variable "api_machine_type" {
  type = string
}

variable "loki_cluster_size" {
  type = number
}

variable "loki_machine_type" {
  type = string
}

variable "build_image_family" {
  type    = string
  default = "e2b-orch"
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

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
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

variable "client_image_family" {
  type    = string
  default = "e2b-orch"
}

variable "client_cluster_name" {
  type    = string
  default = "orch-client"
}

variable "client_clusters_config" {
  description = "Client cluster configurations"
  type = map(object({
    cluster_size = number
    autoscaler = optional(object({
      size_max      = optional(number)
      cpu_target    = optional(number)
      memory_target = optional(number)
    }))
    machine = object({
      type             = string
      min_cpu_platform = string
    })
    boot_disk = object({
      disk_type = string
      size_gb   = number
    })
    cache_disks = object({
      disk_type = string
      size_gb   = number
      count     = number
    })
    hugepages_percentage   = optional(number)
    network_interface_type = optional(string)
    node_labels            = optional(list(string), [])
    subnetwork_name        = optional(string)
    network_tag            = optional(string)
    service_account_email  = optional(string)
  }))
}

variable "build_cluster_name" {
  type    = string
  default = "orch-build"
}

variable "build_clusters_config" {
  description = "Build cluster configurations"
  type = map(object({
    cluster_size = number
    autoscaler = optional(object({
      size_max      = optional(number)
      cpu_target    = optional(number)
      memory_target = optional(number)
    }))
    machine = object({
      type             = string
      min_cpu_platform = string
    })
    boot_disk = object({
      disk_type = string
      size_gb   = number
    })
    cache_disks = object({
      disk_type = string
      size_gb   = number
      count     = number
    })
    hugepages_percentage   = optional(number)
    network_interface_type = optional(string)
    node_labels            = optional(list(string), [])
    subnetwork_name        = optional(string)
    network_tag            = optional(string)
    service_account_email  = optional(string)
  }))
}

variable "gcp_project_id" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "gcp_zone" {
  type = string
}

variable "network_name" {
  type = string
}

variable "google_service_account_email" {
  type = string
}

variable "google_service_account_key" {
  type = string
}

variable "docker_contexts_bucket_name" {
  type = string
}

variable "domain_name" {
  type        = string
  description = "The domain name where e2b will run"
}

variable "additional_domains" {
  type        = list(string)
  description = "Additional domains which can be used to access the e2b cluster"
}

variable "cluster_setup_bucket_name" {
  type        = string
  description = "The name of the bucket to store the setup files"
}

variable "fc_env_pipeline_bucket_name" {
  type        = string
  description = "The name of the bucket to store the files for firecracker environment pipeline"
}

variable "fc_kernels_bucket_name" {
  type        = string
  description = "The name of the bucket to store the kernels for firecracker"
}

variable "fc_versions_bucket_name" {
  type = string
}

variable "fc_busybox_bucket_name" {
  type        = string
  description = "The name of the bucket to store the busybox binary"
}

variable "consul_acl_token_secret" {
  type = string
}

variable "nomad_acl_token_secret" {
  type = string
}

variable "nomad_port" {
  type = number
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
}

variable "clickhouse_cluster_name" {
  type    = string
  default = "clickhouse"
}

variable "clickhouse_cluster_size" {
  description = "The number of ClickHouse nodes in the cluster."
  type        = number
}

variable "clickhouse_machine_type" {
  description = "The machine type of the Compute Instance to run for each node in the cluster."
  type        = string
}


variable "clickhouse_job_constraint_prefix" {
  description = "The prefix to use for the job constraint of the instance in the metadata."
  type        = string
}

variable "clickhouse_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
}

variable "filestore_cache_enabled" {
  type    = bool
  default = false
}

variable "cloudflare_api_token_secret_name" {
  type = string
}

variable "filestore_cache_tier" {
  type    = string
  default = "BASIC_HDD"
}

variable "filestore_cache_capacity_gb" {
  type    = number
  default = 0
}

variable "filestore_nfs_version" {
  type = string
}

variable "api_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
}

variable "build_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
}

variable "clickhouse_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
}

variable "loki_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
}

variable "orchestrator_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
}

variable "api_use_nat" {
  description = "Whether API nodes should use NAT with dedicated external IPs."
  type        = bool
}

variable "api_nat_ips" {
  type        = list(string)
  description = "List of names for static IP addresses to use for NAT. If empty and api_use_nat is true, IPs will be created automatically."
}

variable "api_nat_min_ports_per_vm" {
  type = number
}

# Boot disk type variables
variable "api_boot_disk_type" {
  description = "The GCE boot disk type for the API machines."
  type        = string
}

variable "server_boot_disk_type" {
  description = "The GCE boot disk type for the control server machines."
  type        = string
}

variable "server_boot_disk_size_gb" {
  description = "The GCE boot disk size in GB for the control server machines."
  type        = number
}

variable "clickhouse_boot_disk_type" {
  description = "The GCE boot disk type for the ClickHouse machines."
  type        = string
}

variable "clickhouse_stateful_disk_type" {
  description = "The GCE disk type for the ClickHouse stateful data disk (e.g. pd-ssd, hyperdisk-balanced)."
  type        = string
}

variable "clickhouse_stateful_disk_size_gb" {
  description = "The GCE disk size (in GB) for the ClickHouse stateful data disk."
  type        = number
}

variable "loki_boot_disk_type" {
  description = "The GCE boot disk type for the Loki machines."
  type        = string
}

variable "persistent_volume_types" {
  description = "Persistent volume mount information"
  type = map(object({
    local_mount_path = string
    nfs_location     = string
    nfs_mount_opts   = string
  }))
}

variable "additional_api_paths_handled_by_ingress" {
  type = list(object({
    paths       = list(string)
    timeout_sec = optional(number)
  }))
}

variable "ingress_timeout_seconds" {
  type = number
}

# Per-pool subnetwork overrides
variable "server_subnetwork_name" {
  description = "Subnetwork override for server MIG. Leave empty to use network default."
  type        = string
  default     = ""
}

variable "api_subnetwork_name" {
  description = "Subnetwork override for API MIG. Leave empty to use network default."
  type        = string
  default     = ""
}

variable "clickhouse_subnetwork_name" {
  description = "Subnetwork override for ClickHouse MIG. Leave empty to use network default."
  type        = string
  default     = ""
}

variable "loki_subnetwork_name" {
  description = "Subnetwork override for Loki MIG. Leave empty to use network default."
  type        = string
  default     = ""
}

# Per-pool network tag overrides
variable "server_network_tag" {
  description = "Additional network tag for server MIG."
  type        = string
  default     = ""
}

variable "api_network_tag" {
  description = "Additional network tag for API MIG."
  type        = string
  default     = ""
}

variable "clickhouse_network_tag" {
  description = "Additional network tag for ClickHouse MIG."
  type        = string
  default     = ""
}

variable "loki_network_tag" {
  description = "Additional network tag for Loki MIG."
  type        = string
  default     = ""
}

# Per-pool GSA email overrides
variable "server_service_account_email" {
  description = "GSA email override for server MIG. Defaults to google_service_account_email if empty."
  type        = string
  default     = ""
}

variable "api_service_account_email" {
  description = "GSA email override for API MIG. Defaults to google_service_account_email if empty."
  type        = string
  default     = ""
}

variable "clickhouse_service_account_email" {
  description = "GSA email override for ClickHouse MIG. Defaults to google_service_account_email if empty."
  type        = string
  default     = ""
}

variable "loki_service_account_email" {
  description = "GSA email override for Loki MIG. Defaults to google_service_account_email if empty."
  type        = string
  default     = ""
}
