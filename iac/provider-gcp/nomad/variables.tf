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

variable "client_proxy_oidc_issuer_url" {
  type    = string
  default = ""
}

variable "ingress_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "traefik_config_files" {
  type        = map(string)
  description = "Map of filename => content for additional Traefik dynamic configuration files"
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

variable "api_admin_token_secret_name" {
  type = string
}

variable "dashboard_api_admin_token_secret_name" {
  type = string
}

variable "sandbox_access_token_hash_seed" {
  type = string
}

variable "sandbox_storage_backend" {
  type    = string
  default = "memory"
}

variable "db_max_open_connections" {
  type = number
}

variable "db_min_idle_connections" {
  type = number
}

variable "auth_db_max_open_connections" {
  type = number
}

variable "auth_db_min_idle_connections" {
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

variable "allow_sandbox_internal_cidrs" {
  type        = string
  description = "Comma-separated CIDRs to allow through the sandbox firewall deny list"
  default     = ""
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

variable "persistent_volume_mounts" {
  type = map(string)
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

variable "supabase_db_connection_string_secret_version" {
  type = any
}

variable "auth_provider_config" {
  type = object({
    jwt = optional(list(object({
      issuer = object({
        url                 = string
        discoveryURL        = optional(string)
        audiences           = list(string)
        audienceMatchPolicy = optional(string)
      })
      claimMappings = optional(object({
        username = object({
          claim = string
        })
      }))
      jwksCacheDuration = optional(string)
    })))
    bearer = optional(list(object({
      hmac = object({
        secrets = list(string)
      })
      claimMappings = optional(object({
        username = object({
          claim = string
        })
      }))
    })))
  })
  sensitive = true
  default   = null
}

variable "enable_auth_user_sync_background_worker" {
  type    = bool
  default = false
}

variable "enable_billing_http_team_provision_sink" {
  type    = bool
  default = false
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
  type    = map(string)
  default = {}
}

variable "orchestrator_enabled" {
  type        = bool
  default     = true
  description = "Whether the orchestrator job should be deployed"
}
