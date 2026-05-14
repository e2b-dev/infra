# Variables with defaults that are used directly (not via infra_config)

variable "clickhouse_username" {
  type    = string
  default = "e2b"
}

variable "clickhouse_metrics_port" {
  type    = number
  default = 9363
}

variable "otel_collector_grpc_port" {
  type    = number
  default = 4317
}

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

variable "nomad_autoscaler_version" {
  type        = string
  description = "Version of the Nomad Autoscaler to deploy"
  default     = "0.4.5"
}

# Structured configuration passed from the parent provider-gcp module.

variable "infra_config" {
  description = "Structured configuration from provider-gcp module."
  type = object({
    gcp = object({
      project_id = string
      region     = string
      zone       = string
    })

    prefix      = string
    environment = string
    domain_name = string

    acl = object({
      consul_token = string
      nomad_token  = string
      nomad_port   = number
    })

    service_account = object({
      key                              = string
      docker_reverse_proxy_private_key = string
    })

    secrets = object({
      postgres_connection_string_name    = string
      supabase_jwt_name                  = string
      posthog_api_key_name               = string
      analytics_collector_host_name      = string
      analytics_collector_api_token_name = string
      api_admin_token_name               = string
      dashboard_api_admin_token_name     = string
      launch_darkly_api_key_name         = string
    })

    secret_versions = object({
      redis_cluster_url                       = any
      redis_tls_ca_base64                     = any
      postgres_read_replica_connection_string = any
      supabase_db_connection_string           = any
    })

    storage = object({
      core_repository_name            = string
      template_bucket_name            = string
      build_cache_bucket_name         = string
      fc_env_pipeline_bucket_name     = string
      clickhouse_backups_bucket_name  = string
      loki_bucket_name                = string
      custom_envs_repository_name     = string
      dockerhub_remote_repository_url = string
    })

    generated = object({
      api_secret                     = string
      sandbox_access_token_hash_seed = string
    })

    volume_token = object({
      issuer           = string
      signing_key      = string
      signing_key_name = string
      signing_method   = string
      duration         = string
    })

    infrastructure = object({
      shared_chunk_cache_path  = string
      persistent_volume_mounts = map(string)
    })

    services = object({
      api = object({
        port                         = any
        internal_grpc_port           = number
        resources_cpu_count          = number
        resources_memory_mb          = number
        server_count                 = number
        machine_count                = number
        node_pool                    = string
        db_max_open_connections      = number
        db_min_idle_connections      = number
        auth_db_max_open_connections = number
        auth_db_min_idle_connections = number
        sandbox_storage_backend      = string
      })

      ingress = object({
        port         = any
        count        = number
        config_files = map(string)
      })

      client_proxy = object({
        count               = number
        resources_cpu_count = number
        resources_memory_mb = number
        update_max_parallel = number
        session_port        = number
        health_port         = number
        oidc_issuer_url     = string
      })

      clickhouse = object({
        resources_cpu_count   = number
        resources_memory_mb   = number
        database              = string
        server_count          = number
        server_port           = any
        job_constraint_prefix = string
        node_pool             = string
      })

      orchestrator = object({
        node_pool                      = string
        port                           = number
        proxy_port                     = number
        envd_timeout                   = string
        allow_sandbox_internet         = bool
        allow_sandbox_internal_cidrs   = string
        env_vars                       = map(string)
        enabled                        = bool
        default_persistent_volume_type = string
        gcs_grpc_connection_pool_size  = number
      })

      template_manager = object({
        builder_node_pool  = string
        port               = number
        clusters_size_gt_1 = bool
      })

      loki = object({
        node_pool           = string
        machine_count       = number
        resources_cpu_count = number
        resources_memory_mb = number
        service_port        = any
        use_v13_schema_from = string
      })

      otel_collector = object({
        resources_cpu_count   = number
        resources_memory_mb   = number
        enable_router_logs    = bool
        router_http_port      = number
        enable_router_metrics = bool
        router_grpc_port      = number
      })

      docker_reverse_proxy = object({
        port = any
      })

      redis = object({
        port    = any
        managed = bool
      })

      dashboard_api = object({
        count                                   = number
        enable_auth_user_sync_background_worker = bool
        enable_billing_http_team_provision_sink = bool
      })

      filestore_cache_cleanup = object({
        disk_usage_target     = number
        dry_run               = bool
        deletions_per_loop    = number
        files_per_loop        = number
        max_concurrent_stat   = number
        max_concurrent_scan   = number
        max_concurrent_delete = number
        max_retries           = number
      })
    })
  })

  sensitive = true
}
