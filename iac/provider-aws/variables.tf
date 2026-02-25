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

# --- EKS Configuration ---
variable "kubernetes_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  default     = "1.31"
}

variable "eks_ami_id" {
  description = "Custom AMI ID for EKS nodes (must have kubelet + nested virtualization support)"
  type        = string
}

variable "karpenter_version" {
  description = "Karpenter Helm chart version"
  type        = string
  default     = "1.6.0"
}

variable "client_instance_types" {
  description = "Instance types for the client (orchestrator) Karpenter NodePool"
  type        = list(string)
  default     = ["c8i.2xlarge", "c8i.4xlarge", "c8i.8xlarge"]
}

variable "build_instance_types" {
  description = "Instance types for the build (template-manager) Karpenter NodePool"
  type        = list(string)
  default     = ["c8i.2xlarge", "c8i.4xlarge", "c8i.8xlarge"]
}

variable "boot_disk_size_gb" {
  description = "Boot EBS volume size in GB for Karpenter nodes"
  type        = number
  default     = 100
}

variable "cache_disk_size_gb" {
  description = "Cache EBS volume size in GB for Karpenter nodes"
  type        = number
  default     = 500
}

variable "client_hugepages_percentage" {
  description = "Hugepages percentage for client nodes"
  type        = number
  default     = 80
}

variable "build_hugepages_percentage" {
  description = "Hugepages percentage for build nodes"
  type        = number
  default     = 60
}

# --- API Configuration ---
variable "api_cluster_size" {
  type = number
}

variable "api_resources_cpu_count" {
  type    = number
  default = 2
}

variable "api_resources_memory_mb" {
  type    = number
  default = 2048
}

variable "clickhouse_cluster_size" {
  type = number
}

variable "clickhouse_database_name" {
  description = "The name of the ClickHouse database to create."
  type        = string
  default     = "default"
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

variable "allow_sandbox_internet" {
  type    = bool
  default = true
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

# --- Compliance Services ---
variable "enable_vpc_flow_logs" {
  description = "Enable VPC Flow Logs for network audit trail (GDPR + ISO 27001)"
  type        = bool
  default     = false
}

variable "vpc_flow_logs_retention_days" {
  description = "CloudWatch log group retention in days for VPC Flow Logs"
  type        = number
  default     = 90
}

variable "enable_guardduty" {
  description = "Enable AWS GuardDuty for threat detection (ISO 27001)"
  type        = bool
  default     = false
}

variable "enable_aws_config" {
  description = "Enable AWS Config for configuration compliance monitoring (ISO 27001)"
  type        = bool
  default     = false
}

variable "enable_inspector" {
  description = "Enable AWS Inspector v2 for vulnerability scanning (ISO 27001)"
  type        = bool
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
