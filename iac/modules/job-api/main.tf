locals {
  default_job_env_vars = {
    GIN_MODE             = "release"
    AUTH_PROVIDER_CONFIG = jsonencode(var.auth_provider_config)
  }

  job_env_vars = merge(local.default_job_env_vars, var.job_env_vars)
}

resource "nomad_job" "api" {
  jobspec = templatefile("${path.module}/jobs/api.hcl", {
    update_stanza      = var.update_stanza
    node_pool          = var.node_pool
    prevent_colocation = var.prevent_colocation
    count              = var.count_instances

    memory_mb = var.memory_mb
    cpu_count = var.cpu_count

    domain_name                             = var.domain_name
    environment                             = var.environment
    orchestrator_port                       = var.orchestrator_port
    otel_collector_grpc_endpoint            = var.otel_collector_grpc_endpoint
    logs_collector_address                  = var.logs_collector_address
    port_name                               = var.port_name
    port_number                             = var.port_number
    api_internal_grpc_port                  = var.api_internal_grpc_port
    api_docker_image                        = var.api_docker_image
    postgres_connection_string              = var.postgres_connection_string
    postgres_read_replica_connection_string = var.postgres_read_replica_connection_string
    posthog_api_key                         = var.posthog_api_key
    analytics_collector_host                = var.analytics_collector_host
    analytics_collector_api_token           = var.analytics_collector_api_token
    nomad_acl_token                         = var.nomad_acl_token
    admin_token                             = var.admin_token
    redis_url                               = var.redis_url
    redis_cluster_url                       = var.redis_cluster_url
    redis_tls_ca_base64                     = var.redis_tls_ca_base64
    db_max_open_connections                 = var.db_max_open_connections
    db_min_idle_connections                 = var.db_min_idle_connections
    auth_db_max_open_connections            = var.auth_db_max_open_connections
    auth_db_min_idle_connections            = var.auth_db_min_idle_connections
    redis_pool_size                         = var.redis_pool_size
    clickhouse_connection_string            = var.clickhouse_connection_string
    loki_url                                = var.loki_url
    sandbox_access_token_hash_seed          = var.sandbox_access_token_hash_seed
    sandbox_storage_backend                 = var.sandbox_storage_backend
    db_migrator_docker_image                = var.db_migrator_docker_image
    launch_darkly_api_key                   = trimspace(var.launch_darkly_api_key)
    default_persistent_volume_type          = var.default_persistent_volume_type
    job_env_vars                            = local.job_env_vars
  })
}
