variable "prefix" {
  type = string
}

variable "aws_region" {
  type = string
}

variable "environment" {
  type = string
}

variable "domain_name" {
  type = string
}

variable "core_repository_url" {
  type        = string
  description = "ECR repository URL for core images"
}

# API
variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "api_grpc_port" {
  type    = number
  default = 5009
}

variable "api_resources_cpu_count" {
  type = number
}

variable "api_resources_memory_mb" {
  type = number
}

variable "api_machine_count" {
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

# Secret ARNs
variable "postgres_connection_string_secret_arn" {
  type = string
}

variable "postgres_read_replica_connection_string_secret_arn" {
  type = string
}

variable "supabase_jwt_secrets_secret_arn" {
  type = string
}

variable "posthog_api_key_secret_arn" {
  type = string
}

variable "analytics_collector_host_secret_arn" {
  type = string
}

variable "analytics_collector_api_token_secret_arn" {
  type = string
}

variable "launch_darkly_api_key_secret_arn" {
  type = string
}

variable "redis_cluster_url_secret_arn" {
  type = string
}

variable "redis_tls_ca_base64_secret_arn" {
  type = string
}

# Ingress
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

# Client Proxy
variable "client_proxy_count" {
  type = number
}

variable "client_proxy_resources_cpu_count" {
  type = number
}

variable "client_proxy_resources_memory_mb" {
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

# Docker reverse proxy
variable "docker_reverse_proxy_count" {
  description = "Number of docker-reverse-proxy replicas"
  type        = number
  default     = 2
}

variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
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

variable "envd_timeout" {
  type = string
}

# Template manager
variable "template_manager_port" {
  type = number
}

variable "template_bucket_name" {
  type = string
}

variable "build_cache_bucket_name" {
  type = string
}

# Logs
variable "loki_machine_count" {
  type = number
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

variable "loki_use_v13_schema_from" {
  type    = string
  default = ""
}

# Otel Collector
variable "otel_collector_resources_memory_mb" {
  type = number
}

variable "otel_collector_resources_cpu_count" {
  type = number
}

variable "otel_collector_grpc_port" {
  type    = number
  default = 4317
}

# Clickhouse
variable "clickhouse_resources_cpu_count" {
  type = number
}

variable "clickhouse_resources_memory_mb" {
  type = number
}

variable "clickhouse_database" {
  type = string
}

variable "clickhouse_backups_bucket_name" {
  type = string
}

variable "clickhouse_server_count" {
  type = number
}

variable "clickhouse_server_port" {
  type = object({
    name = string
    port = number
  })
}

variable "clickhouse_username" {
  type    = string
  default = "e2b"
}

variable "clickhouse_metrics_port" {
  type    = number
  default = 9363
}

# Redis
variable "redis_managed" {
  type = bool
}

variable "redis_port" {
  type = object({
    name = string
    port = number
  })
}

# Logs proxy
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

# Filestore / EFS cache
variable "shared_chunk_cache_path" {
  type    = string
  default = ""
}

variable "filestore_cache_cleanup_disk_usage_target" {
  type = number
}

variable "filestore_cache_cleanup_dry_run" {
  type = bool
}

variable "filestore_cache_cleanup_deletions_per_loop" {
  type = number
}

variable "filestore_cache_cleanup_files_per_loop" {
  type = number
}

variable "filestore_cache_cleanup_max_concurrent_stat" {
  type = number
}

variable "filestore_cache_cleanup_max_concurrent_scan" {
  type = number
}

variable "filestore_cache_cleanup_max_concurrent_delete" {
  type = number
}

variable "filestore_cache_cleanup_max_retries" {
  type = number
}

variable "dockerhub_remote_repository_url" {
  type    = string
  default = ""
}
