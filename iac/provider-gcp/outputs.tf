# Outputs for use by the nomad module when imported separately
#
# This structured output packages all values that the nomad module needs,
# allowing it to be used as a standalone module by passing this config object.

locals {
  nomad_config = {
    # GCP Context
    gcp = {
      project_id = var.gcp_project_id
      region     = var.gcp_region
      zone       = var.gcp_zone
    }

    # General
    prefix      = var.prefix
    environment = var.environment
    domain_name = var.domain_name

    # ACL Tokens
    acl = {
      consul_token = module.init.consul_acl_token_secret
      nomad_token  = module.init.nomad_acl_token_secret
      nomad_port   = var.nomad_port
    }

    # Service Account
    service_account = {
      key                              = module.init.google_service_account_key
      docker_reverse_proxy_private_key = google_service_account_key.google_service_key.private_key
    }

    # Secrets (names for GCP Secret Manager lookups)
    secrets = {
      postgres_connection_string_name         = module.init.postgres_connection_string_secret_name
      supabase_jwt_name                       = module.init.supabase_jwt_secret_name
      posthog_api_key_name                    = module.init.posthog_api_key_secret_name
      analytics_collector_host_name           = module.init.analytics_collector_host_secret_name
      analytics_collector_api_token_name      = module.init.analytics_collector_api_token_secret_name
      api_admin_token_name                    = module.init.api_admin_token_secret_name
      dashboard_api_admin_token_name          = module.init.dashboard_api_admin_token_secret_name
      launch_darkly_api_key_name              = module.init.launch_darkly_api_key_secret_version.secret
    }

    # Secret Versions (full version resources for direct access)
    secret_versions = {
      redis_cluster_url                       = module.init.redis_cluster_url_secret_version
      redis_tls_ca_base64                     = module.init.redis_tls_ca_base64_secret_version
      postgres_read_replica_connection_string = google_secret_manager_secret_version.postgres_read_replica_connection_string
      supabase_db_connection_string           = module.init.supabase_db_connection_string_secret_version
    }

    # Storage Buckets & Repositories
    storage = {
      core_repository_name            = module.init.core_repository_name
      template_bucket_name            = module.init.fc_template_bucket_name
      build_cache_bucket_name         = module.init.fc_build_cache_bucket_name
      fc_env_pipeline_bucket_name     = module.init.fc_env_pipeline_bucket_name
      clickhouse_backups_bucket_name  = module.init.clickhouse_backups_bucket_name
      loki_bucket_name                = module.init.loki_bucket_name
      custom_envs_repository_name     = google_artifact_registry_repository.custom_environments_repository.name
      dockerhub_remote_repository_url = var.remote_repository_enabled ? module.remote_repository[0].dockerhub_remote_repository_url : ""
    }

    # Generated Values
    generated = {
      api_secret                     = random_password.api_secret.result
      sandbox_access_token_hash_seed = random_password.sandbox_access_token_hash_seed.result
    }

    # Volume Token Configuration
    volume_token = {
      issuer           = local.volume_token_issuer
      signing_key      = local.volume_token_signing_key
      signing_key_name = local.volume_token_signature_name
      signing_method   = local.volume_token_signature_method
      duration         = var.volume_token_valid_for
    }

    # Infrastructure (from cluster module)
    infrastructure = {
      shared_chunk_cache_path = module.cluster.shared_chunk_cache_path
      persistent_volume_mounts = { for key, config in local.persistent_volume_types : key => config["local_mount_path"] }
    }

    # Service Configurations (pass-through from variables)
    services = {
      # API
      api = {
        port                     = var.api_port
        internal_grpc_port       = var.api_internal_grpc_port
        resources_cpu_count      = var.api_resources_cpu_count
        resources_memory_mb      = var.api_resources_memory_mb
        server_count             = var.api_server_count
        machine_count            = var.api_cluster_size
        node_pool                = var.api_node_pool
        db_max_open_connections  = var.db_max_open_connections
        db_min_idle_connections  = var.db_min_idle_connections
        auth_db_max_open_connections = var.auth_db_max_open_connections
        auth_db_min_idle_connections = var.auth_db_min_idle_connections
        sandbox_storage_backend  = var.sandbox_storage_backend
      }

      # Ingress
      ingress = {
        port         = var.ingress_port
        count        = var.ingress_count
        config_files = var.traefik_config_files
      }

      # Client Proxy
      client_proxy = {
        count                = var.client_proxy_count
        resources_cpu_count  = var.client_proxy_resources_cpu_count
        resources_memory_mb  = var.client_proxy_resources_memory_mb
        update_max_parallel  = var.client_proxy_update_max_parallel
        session_port         = var.client_proxy_port.port
        health_port          = var.client_proxy_health_port.port
        oidc_issuer_url      = var.client_proxy_oidc_issuer_url
      }

      # ClickHouse
      clickhouse = {
        resources_cpu_count      = var.clickhouse_resources_cpu_count
        resources_memory_mb      = var.clickhouse_resources_memory_mb
        database                 = var.clickhouse_database_name
        server_count             = var.clickhouse_cluster_size
        server_port              = var.clickhouse_server_service_port
        job_constraint_prefix    = var.clickhouse_job_constraint_prefix
        node_pool                = var.clickhouse_node_pool
      }

      # Orchestrator
      orchestrator = {
        node_pool                = var.orchestrator_node_pool
        port                     = var.orchestrator_port
        proxy_port               = var.orchestrator_proxy_port
        envd_timeout             = var.envd_timeout
        allow_sandbox_internet   = var.allow_sandbox_internet
        allow_sandbox_internal_cidrs = var.allow_sandbox_internal_cidrs
        env_vars                 = var.orchestrator_env_vars
        enabled                  = var.orchestrator_enabled
        default_persistent_volume_type = var.default_persistent_volume_type
        gcs_grpc_connection_pool_size = var.gcs_grpc_connection_pool_size
      }

      # Template Manager
      template_manager = {
        builder_node_pool           = var.build_node_pool
        port                        = var.template_manager_port
        clusters_size_gt_1          = local.template_manages_clusters_size_gt_1
      }

      # Loki
      loki = {
        node_pool           = var.loki_node_pool
        machine_count       = var.loki_cluster_size
        resources_cpu_count = var.loki_resources_cpu_count
        resources_memory_mb = var.loki_resources_memory_mb
        service_port        = var.loki_service_port
        use_v13_schema_from = var.loki_use_v13_schema_from
      }

      # OTEL Collector
      otel_collector = {
        resources_cpu_count    = var.otel_collector_resources_cpu_count
        resources_memory_mb    = var.otel_collector_resources_memory_mb
        enable_router_logs     = var.enable_otel_router_logs
        router_http_port       = var.otel_router_http_port
        enable_router_metrics  = var.enable_otel_router_metrics
        router_grpc_port       = var.otel_router_grpc_port
      }

      # Docker Reverse Proxy
      docker_reverse_proxy = {
        port = var.docker_reverse_proxy_port
      }

      # Redis
      redis = {
        port    = var.redis_port
        managed = var.redis_managed
      }

      # Dashboard API
      dashboard_api = {
        count                                  = var.dashboard_api_count
        enable_auth_user_sync_background_worker = var.enable_auth_user_sync_background_worker
        enable_billing_http_team_provision_sink = var.enable_billing_http_team_provision_sink
      }

      # Filestore Cache Cleanup
      filestore_cache_cleanup = {
        disk_usage_target     = var.filestore_cache_cleanup_disk_usage_target
        dry_run               = var.filestore_cache_cleanup_dry_run
        deletions_per_loop    = var.filestore_cache_cleanup_deletions_per_loop
        files_per_loop        = var.filestore_cache_cleanup_files_per_loop
        max_concurrent_stat   = var.filestore_cache_cleanup_max_concurrent_stat
        max_concurrent_scan   = var.filestore_cache_cleanup_max_concurrent_scan
        max_concurrent_delete = var.filestore_cache_cleanup_max_concurrent_delete
        max_retries           = var.filestore_cache_cleanup_max_retries
      }
    }
  }
}

output "nomad_config" {
  description = "Configuration object containing all values needed by the nomad module"
  sensitive   = true

  value = local.nomad_config
}
