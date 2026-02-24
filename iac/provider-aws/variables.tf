variable "aws_region" {
  description = "The AWS region to deploy resources in"
  type        = string
}

variable "availability_zones" {
  description = "List of availability zones to use"
  type        = list(string)
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "ami_id" {
  description = "AMI ID for the cluster instances (must have Nomad/Consul pre-installed)"
  type        = string
}

variable "server_cluster_size" {
  type = number
}

variable "server_instance_type" {
  description = "EC2 instance type for Nomad server nodes"
  type        = string
}

variable "api_cluster_size" {
  type = number
}

variable "api_instance_type" {
  description = "EC2 instance type for API nodes"
  type        = string
}

variable "api_node_pool" {
  type    = string
  default = "api"
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
  default = 5008
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

variable "domain_name" {
  type        = string
  description = "The domain name where e2b will run"
}

variable "prefix" {
  type        = string
  description = "The prefix to use for all resources in this module"
  default     = "e2b-"
}

variable "bucket_prefix" {
  type = string
}

variable "tags" {
  description = "Tags to apply to all resources"
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

variable "template_bucket_name" {
  type        = string
  description = "The name of the FC template bucket"
  default     = ""
}

variable "redis_managed" {
  default = false
  type    = bool
}

variable "redis_node_type" {
  description = "ElastiCache node type for Redis"
  type        = string
  default     = "cache.t3.medium"
}

variable "redis_shard_count" {
  description = "Number of shards in the Redis cluster"
  type        = number
  default     = 1
}

variable "redis_replica_count" {
  description = "Number of replicas per shard"
  type        = number
  default     = 1
}

variable "efs_cache_enabled" {
  type        = bool
  description = "Set to true to enable EFS shared cache (replaces GCP Filestore)"
  default     = false
}

variable "filestore_cache_cleanup_disk_usage_target" {
  type        = number
  description = "The max disk usage target of the shared cache in percent"
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

variable "client_clusters_config" {
  type = map(object({
    cluster_size = number

    instance_type = string

    autoscaler = optional(object({
      max_size   = optional(number)
      cpu_target = optional(number)
    }))

    boot_disk_size_gb = number
    boot_disk_type    = optional(string, "gp3")

    cache_disks = object({
      type    = string
      size_gb = number
      count   = number
    })

    hugepages_percentage = optional(number)
  }))

  description = <<EOT
Configuration for the client clusters.
Format: {
  "default" = {
    cluster_size  = 1
    instance_type = "i3.metal"   # Must be .metal for Firecracker KVM
    autoscaler = {
      max_size   = 3
      cpu_target = 70
    }
    boot_disk_size_gb = 100
    cache_disks = {
      type    = "nvme"       # "nvme" for instance store, "ebs" for EBS
      size_gb = 1900
      count   = 1
    }
    hugepages_percentage = 80
  }
}
EOT
}

variable "build_clusters_config" {
  type = map(object({
    cluster_size = number

    instance_type = string

    autoscaler = optional(object({
      max_size   = optional(number)
      cpu_target = optional(number)
    }))

    boot_disk_size_gb = number
    boot_disk_type    = optional(string, "gp3")

    cache_disks = object({
      type    = string
      size_gb = number
      count   = number
    })

    hugepages_percentage = optional(number)
  }))

  description = <<EOT
Configuration for the build clusters.
Format: {
  "default" = {
    cluster_size  = 1
    instance_type = "i3.metal"   # Must be .metal for Firecracker KVM
    autoscaler = {
      max_size   = 3
      cpu_target = 70
    }
    boot_disk_size_gb = 100
    cache_disks = {
      type    = "nvme"
      size_gb = 1900
      count   = 1
    }
    hugepages_percentage = 60
  }
}
EOT
}

variable "loki_use_v13_schema_from" {
  type        = string
  description = "This should be a date soon after you deploy. Format = YYYY-MM-DD"
  default     = ""

  validation {
    condition     = var.loki_use_v13_schema_from == "" || can(regex("\\d{4}-\\d{2}-\\d{2}", var.loki_use_v13_schema_from))
    error_message = "must be YYYY-MM-DD"
  }
}

variable "dockerhub_remote_repository_url" {
  type    = string
  default = ""
}
