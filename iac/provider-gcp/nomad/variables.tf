variable "prefix" {
  type = string
}

variable "gcp_zone" {
  type = string
}

variable "orchestrator_node_pool" {
  type = string
}

variable "core_repository_name" {
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

# API
variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "api_internal_grpc_port" {
  type    = number
  default = 5009
}

variable "api_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}

variable "api_db_migrator_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}

variable "ingress_port" {
  type        = number
  description = "External traffic port number"
}

variable "ingress_internal_port" {
  type        = number
  description = "Internal traffic port number"
}

variable "ingress_count" {
  type = number
}

variable "traefik_config_files" {
  type        = map(string)
  description = "Map of filename => content for additional Traefik dynamic configuration files"
}

variable "api_resources_cpu_count" {
  type = number
}

variable "api_resources_memory_mb" {
  type = number
}

variable "environment" {
  type = string
}

variable "api_server_count" {
  type = number
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

variable "postgres_connection_string_secret_name" {
  type = string
}

variable "postgres_read_replica_connection_string_secret_version" {
  type = any
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

variable "client_proxy_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
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

# Docker reverse proxy
variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "docker_reverse_proxy_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
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

# Template manager
variable "template_manager_port" {
  type = number
}

variable "template_manager_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
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

variable "clickhouse_password" {
  type      = string
  sensitive = true
}

variable "clickhouse_server_secret" {
  type      = string
  sensitive = true
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

variable "enable_otel_router_logs" {
  type        = bool
  default     = false
  description = "Enable teeing non-internal customer logs from Vector to otel-router."
}

variable "otel_router_http_port" {
  type        = number
  default     = 4321
  description = "Local otel-router Vector-compatible logs port used by Vector when otel-router log teeing is enabled."
}

variable "enable_otel_router_metrics" {
  type        = bool
  default     = false
  description = "Enable teeing external customer metrics from otel-collector to otel-router."
}

variable "otel_router_grpc_port" {
  type        = number
  default     = 4320
  description = "Local otel-router OTLP gRPC port used by otel-collector when otel-router metric teeing is enabled."
}

variable "enable_gcp_telemetry_metrics" {
  type        = bool
  default     = false
  description = "Enable exporting selected otel-collector metrics to Google Cloud Monitoring using the googlecloud exporter."
}

variable "enable_gcp_telemetry_external_metrics" {
  type        = bool
  default     = false
  description = "Enable exporting external e2b.* metrics to Google Cloud Monitoring. Requires enable_gcp_telemetry_metrics."
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

variable "filestore_cleanup_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}

variable "dockerhub_remote_repository_url" {
  type = string
}

variable "default_persistent_volume_type" {
  type    = string
  default = ""
}

# Dashboard API
variable "dashboard_api_count" {
  type    = number
  default = 0
}

variable "dashboard_api_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}

variable "volume_token_issuer" {
  type = string
}

variable "volume_token_signing_key" {
  type = string
}

variable "volume_token_signing_key_name" {
  type = string
}

variable "volume_token_signing_method" {
  type = string
}

variable "volume_token_duration" {
  type = string
}

variable "gcs_grpc_connection_pool_size" {
  description = "Number of gRPC connections in the GCS connection pool"
  type        = number
}

variable "orchestrator_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}

variable "orchestrator_enabled" {
  type        = bool
  default     = true
  description = "Whether the orchestrator job should be deployed"
}
