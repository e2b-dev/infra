variable "envd_timeout" {
  type = string
}

variable "prefix" {
  type = string
}

variable "gcp_zone" {
  type = string
}

variable "orchestrator_node_pool" {
  type = string
}

variable "orchestration_repository_name" {
  type = string
}

variable "consul_acl_token_secret" {
  type = string
}

variable "template_bucket_name" {
  type = string
}

variable "build_cache_bucket_name" {
  type = string
}

variable "builder_node_pool" {
  type = string
}


variable "nomad_acl_token_secret" {
  type = string
}

variable "nomad_port" {
  type = number
}

variable "otel_collector_resources_memory_mb" {
  type = number
}

variable "otel_collector_resources_cpu_count" {
  type = number
}

variable "otel_tracing_print" {
  type = bool
}

# API
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

variable "ingress_count" {
  type = number
}

variable "api_resources_cpu_count" {
  type = number
}

variable "api_resources_memory_mb" {
  type = number
}

variable "api_secret" {
  type = string
}

variable "api_admin_token" {
  type = string
}

variable "sandbox_access_token_hash_seed" {
  type = string
}

variable "environment" {
  type = string
}

variable "api_machine_count" {
  type = number
}

variable "api_node_pool" {
  type = string
}

variable "loki_use_v13_schema_from" {
  type    = string
  default = ""
}

variable "loki_machine_count" {
  type = number
}

variable "loki_node_pool" {
  type = string
}

variable "custom_envs_repository_name" {
  type = string
}

variable "gcp_project_id" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "google_service_account_key" {
  type = string
}

variable "posthog_api_key_secret_name" {
  type = string
}

variable "postgres_connection_string_secret_name" {
  type = string
}

variable "postgres_read_replica_connection_string_secret_version" {
  type = any
}

variable "supabase_jwt_secrets_secret_name" {
  type = string
}

variable "client_proxy_count" {
  type = number
}

variable "client_proxy_resources_memory_mb" {
  type = number
}

variable "client_proxy_resources_cpu_count" {
  type = number
}

variable "client_proxy_update_max_parallel" {
  type = number
}

variable "client_proxy_session_port" {
  type = number
}

variable "client_proxy_health_port" {
  type = number
}

variable "domain_name" {
  type = string
}

# Telemetry
variable "logs_proxy_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "logs"
    port = 30006
  }
}

variable "logs_health_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "logs-health"
    port        = 44313
    health_path = "/health"
  }
}

variable "analytics_collector_host_secret_name" {
  type = string
}

variable "analytics_collector_api_token_secret_name" {
  type = string
}

variable "launch_darkly_api_key_secret_name" {
  type = string
}

variable "clickhouse_backups_bucket_name" {
  type = string
}

variable "loki_resources_memory_mb" {
  type = number
}

variable "loki_resources_cpu_count" {
  type = number
}

variable "loki_bucket_name" {
  type = string
}

variable "loki_service_port" {
  type = object({
    name = string
    port = number
  })
}

variable "redis_cluster_url_secret_version" {
  type = any
}

variable "redis_tls_ca_base64_secret_version" {
  type = any
}

# Docker reverse proxy
variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "docker_reverse_proxy_service_account_key" {
  type = string
}

# Orchestrator
variable "orchestrator_port" {
  type = number
}

variable "orchestrator_proxy_port" {
  type = number
}

variable "fc_env_pipeline_bucket_name" {
  type = string
}

variable "allow_sandbox_internet" {
  type = bool
}

# Template manager
variable "template_manager_port" {
  type = number
}

variable "template_manages_clusters_size_gt_1" {
  type = bool
}

variable "nomad_autoscaler_version" {
  type        = string
  description = "Version of the Nomad Autoscaler to deploy"
  default     = "0.4.5"
}

# Redis
variable "redis_port" {
  type = object({
    name = string
    port = number
  })
}

variable "redis_managed" {
  type = bool
}

# Clickhouse
variable "clickhouse_resources_memory_mb" {
  type = number
}

variable "clickhouse_resources_cpu_count" {
  type = number
}

variable "clickhouse_username" {
  type    = string
  default = "e2b"
}

variable "clickhouse_database" {
  type = string
}

variable "clickhouse_server_count" {
  type = number
}

variable "clickhouse_metrics_port" {
  type    = number
  default = 9363
}

variable "otel_collector_grpc_port" {
  type    = number
  default = 4317
}
variable "clickhouse_server_port" {
  type = object({
    name = string
    port = number
  })
}

variable "clickhouse_job_constraint_prefix" {
  description = "The prefix to use for the job constraint of the instance in the metadata."
  type        = string
}

variable "clickhouse_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
}

variable "shared_chunk_cache_path" {
  type    = string
  default = ""
}

variable "filestore_cache_cleanup_disk_usage_target" {
  type        = number
  description = "The disk usage target for the Filestore cache in percent"
  validation {
    condition     = var.filestore_cache_cleanup_disk_usage_target >= 0 && var.filestore_cache_cleanup_disk_usage_target < 100
    error_message = "Must be between 0 and 100"
  }
}

variable "filestore_cache_cleanup_dry_run" {
  type = bool
}

variable "filestore_cache_cleanup_deletions_per_loop" {
  type = number
  validation {
    condition     = var.filestore_cache_cleanup_deletions_per_loop > 0
    error_message = "Must be greater than 0"
  }
}

variable "filestore_cache_cleanup_files_per_loop" {
  type = number
}

variable "filestore_cache_cleanup_max_concurrent_stat" {
  type        = number
  description = "Number of concurrent stat goroutines"
}

variable "filestore_cache_cleanup_max_concurrent_scan" {
  type        = number
  description = "Number of concurrent scanner goroutines"
}

variable "filestore_cache_cleanup_max_concurrent_delete" {
  type        = number
  description = "Number of concurrent deleter goroutines"
}

variable "filestore_cache_cleanup_max_retries" {
  type        = number
  description = "Maximum number of continuous error or miss retries before giving up"
}

variable "dockerhub_remote_repository_url" {
  type = string
}
