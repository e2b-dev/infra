variable "gcp_project_id" {
  description = "The project to deploy the cluster in"
  type        = string
}

variable "gcp_region" {
  type = string
}

variable "gcp_zone" {
  description = "All GCP resources will be launched in this Zone."
  type        = string
}

variable "server_cluster_size" {
  type = number
}

variable "server_machine_type" {
  type = string
}

variable "api_cluster_size" {
  type = number
}

variable "api_machine_type" {
  type = string
}

variable "api_node_pool" {
  type    = string
  default = "api"
}

variable "api_use_nat" {
  type        = bool
  description = "Whether API nodes should use NAT with dedicated external IPs."
  default     = false
}

variable "api_nat_ips" {
  type        = list(string)
  description = "List of names for static IP addresses to use for NAT. If empty and api_use_nat is true, IPs will be created automatically."
  default     = []
}

variable "api_nat_min_ports_per_vm" {
  type        = number
  description = "The default API NAT minimum ports per VM."
  default     = 170
}

variable "api_resources_cpu_count" {
  type    = number
  default = 2
}

variable "api_resources_memory_mb" {
  type    = number
  default = 2048
}

variable "build_node_pool" {
  type    = string
  default = "build"
}

variable "clickhouse_cluster_size" {
  type = number
}

variable "clickhouse_machine_type" {
  type = string
}

variable "clickhouse_database_name" {
  description = "The name of the ClickHouse database to create."
  type        = string
  default     = "default"
}

variable "clickhouse_job_constraint_prefix" {
  description = "The prefix to use for the job constraint of the instance in the metadata."
  type        = string
  default     = "clickhouse"
}

variable "clickhouse_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
  default     = "clickhouse"
}

variable "clickhouse_server_service_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "clickhouse"
    port = 9000
  }
}

variable "clickhouse_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
  default = {
    name = "clickhouse-health"
    port = 8123
    path = "/health"
  }
}

variable "client_proxy_count" {
  type    = number
  default = 1
}

variable "ingress_count" {
  type    = number
  default = 1
}

variable "client_proxy_resources_memory_mb" {
  type    = number
  default = 1024
}

variable "client_proxy_resources_cpu_count" {
  type    = number
  default = 1
}

variable "client_proxy_update_max_parallel" {
  type        = number
  description = "The number of client proxies to update in parallel during a rolling update."
  default     = 1
}

variable "client_proxy_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
  default = {
    name = "client-proxy"
    port = 3001
    path = "/health"
  }
}

variable "client_proxy_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "session"
    port = 3002
  }
}

variable "loki_cluster_size" {
  type    = number
  default = 0
}

variable "loki_machine_type" {
  type    = string
  default = "e2-standard-4"
}

variable "loki_node_pool" {
  type    = string
  default = "loki"
}

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "api"
    port        = 50001
    health_path = "/health"
  }
}

variable "ingress_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "ingress"
    port        = 8800
    health_path = "/ping"
  }
}

variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "docker-reverse-proxy"
    port        = 5000
    health_path = "/health"
  }
}

variable "redis_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "redis"
    port = 6379
  }
}

variable "nomad_port" {
  type    = number
  default = 4646
}

variable "allow_sandbox_internet" {
  type    = bool
  default = true
}

variable "orchestrator_node_pool" {
  type    = string
  default = "default"
}

variable "orchestrator_port" {
  type    = number
  default = 5008
}

variable "orchestrator_proxy_port" {
  type    = number
  default = 5007
}

variable "template_manager_port" {
  type    = number
  default = 5008 // we want to use the same port for both because of edge api
}

variable "envd_timeout" {
  type    = string
  default = "40s"
}

variable "environment" {
  type    = string
  default = "prod"
}

variable "otel_collector_resources_memory_mb" {
  type    = number
  default = 1024
}

variable "otel_collector_resources_cpu_count" {
  type    = number
  default = 0.5
}

variable "clickhouse_resources_memory_mb" {
  type    = number
  default = 8192
}

variable "clickhouse_resources_cpu_count" {
  type    = number
  default = 4
}

variable "otel_tracing_print" {
  description = "Whether to print OTEL traces to stdout"
  type        = bool
  default     = false
}

variable "domain_name" {
  type        = string
  description = "The domain name where e2b will run"
}

variable "additional_api_services_json" {
  type        = string
  description = <<EOT
Additional path rules to add to the API path matcher.
Format: json string of an array of objects with 'path' and 'service' keys.
Example:
[
  {
    "paths": ["/api/v1"],
    "service_id": "projects/e2b/global/backendServices/example",
    "api_node_group_port_name": "example-port",
    "api_node_group_port": 8080
  }
]
EOT
  default     = ""
}

variable "prefix" {
  type        = string
  description = "The prefix to use for all resources in this module"
  default     = "e2b-"
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
  default = {
    "app"       = "e2b"
    "terraform" = "true"
  }
}

variable "loki_resources_memory_mb" {
  type    = number
  default = 2048
}

variable "loki_resources_cpu_count" {
  type    = number
  default = 1
}

variable "loki_service_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "loki"
    port = 3100
  }
}

variable "template_bucket_location" {
  type        = string
  description = "The location of the FC template bucket"
}

variable "template_bucket_name" {
  type        = string
  description = "The name of the FC template bucket"
  default     = ""
}

variable "redis_managed" {
  default = false
  type    = bool
}

variable "filestore_cache_enabled" {
  type        = bool
  description = "Set to true to enable Filestore cache. Can be set via TF_VAR_use_filestore_cache or USE_FILESTORE_CACHE env var."
  default     = false
}

variable "filestore_cache_tier" {
  type        = string
  description = "The tier of the Filestore cache"
  default     = "BASIC_HDD"
}

variable "filestore_cache_capacity_gb" {
  type        = number
  description = "The capacity of the Filestore cache in GB"
  default     = 0
}

variable "filestore_cache_cleanup_disk_usage_target" {
  type        = number
  description = "The max disk usage target of the Filestore"
  default     = 90
}

variable "filestore_cache_cleanup_dry_run" {
  type    = bool
  default = false
}

variable "filestore_cache_cleanup_files_per_loop" {
  type    = number
  default = 10000
}

variable "filestore_cache_cleanup_deletions_per_loop" {
  type    = number
  default = 900
}

variable "filestore_cache_cleanup_max_concurrent_stat" {
  type        = number
  description = "Number of concurrent stat goroutines"
  default     = 16
}

variable "filestore_cache_cleanup_max_concurrent_scan" {
  type        = number
  description = "Number of concurrent scanner goroutines"
  default     = 16
}

variable "filestore_cache_cleanup_max_concurrent_delete" {
  type        = number
  description = "Number of concurrent deleter goroutines"
  default     = 4
}

variable "filestore_cache_cleanup_max_retries" {
  type        = number
  description = "Maximum number of continuous error or miss retries before giving up"
  default     = 10000
}

variable "remote_repository_enabled" {
  type        = bool
  description = "Set to true to enable remote repository cache. Can be set via TF_VAR_remote_repository_enabled or REMOTE_REPOSITORY_ENABLED env var."
  default     = false
}

variable "client_clusters_config" {
  type = map(object({
    cluster_size = number

    machine = object({
      type             = string
      min_cpu_platform = string
    })

    autoscaler = optional(object({
      size_max      = optional(number)
      memory_target = optional(number)
      cpu_target    = optional(number)
    }))

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
  }))

  description = <<EOT
Configuration for the client clusters.
Format: [
  {
      "cluster_size": 1,  // Number of nodes (the actual number of nodes may be higher due to autoscaling)
      "machine": {   // Machine type and CPU platform
          "type": "n1-standard-8",
          "min_cpu_platform": "Intel Skylake"
      },
      "autoscaler": {
          "size_max": 1, // Maximum number of nodes to scale up to
          "memory_target": 100,  // Target memory utilization percentage for autoscaling (0-100)
          "cpu_target": 0.7  // Target CPU utilization percentage for autoscaling (0-1)
      },
      "boot_disk": {
          "disk_type": "pd-ssd",  // Boot disk type
          "size_gb": 100  // Boot disk size in GB
      },
      "cache_disks": {
          "disk_type": "local-ssd",  // Cache disk type
          "size_gb": 375,  // Cache disk size in GB
          "count": 3  // Number of cache disks
      },
      "hugepages_percentage": 80
  }
]
EOT
}

variable "build_clusters_config" {
  type = map(object({
    cluster_size = number

    machine = object({
      type             = string
      min_cpu_platform = string
    })

    autoscaler = optional(object({
      size_max      = optional(number)
      memory_target = optional(number)
      cpu_target    = optional(number)
    }))

    boot_disk = object({
      disk_type = string
      size_gb   = number
    })

    cache_disks = object({
      disk_type = string
      size_gb   = string
      count     = number
    })

    hugepages_percentage   = optional(number)
    network_interface_type = optional(string)
  }))
  description = <<EOT
Configuration for the build clusters.
Format:
[
  {
    "cluster_size": 1,  // Number of nodes (the actual number of nodes may be higher due to autoscaling)
    "machine": {   // Machine type and CPU platform
        "type": "n1-standard-8",
        "min_cpu_platform": "Intel Skylake"
    },
    "autoscaler": {
        "size_max": 1, // Maximum number of nodes to scale up to
        "memory_target": 100,  // Target memory utilization percentage for autoscaling (0-100)
        "cpu_target": 0.7  // Target CPU utilization percentage for autoscaling (0-1)
    },
    "boot_disk": {
        "disk_type": "pd-ssd",  // Boot disk type
        "size_gb": 100  // Boot disk size in GB
    },
    "cache_disks": {
        "disk_type": "local-ssd",  // Cache disk type
        "size_gb": 375,  // Cache disk size in GB
        "count": 3  // Number of cache disks
    },
    "hugepages_percentage": 60
  }
]
EOT
}

# Boot disk type variables
variable "api_boot_disk_type" {
  description = "The GCE boot disk type for the API machines."
  type        = string
  default     = "pd-ssd"
}

variable "server_boot_disk_type" {
  description = "The GCE boot disk type for the control server machines."
  type        = string
  default     = "pd-ssd"
}

variable "server_boot_disk_size_gb" {
  description = "The GCE boot disk size (in GB) for the control server machines."
  type        = number
  default     = 20
}

variable "clickhouse_boot_disk_type" {
  description = "The GCE boot disk type for the ClickHouse machines."
  type        = string
  default     = "pd-ssd"
}

variable "loki_boot_disk_type" {
  description = "The GCE boot disk type for the Loki machines."
  type        = string
  default     = "pd-ssd"
}
