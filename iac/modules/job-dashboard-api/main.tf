locals {
  base_env = {
    GIN_MODE                               = "release"
    ENVIRONMENT                            = var.environment
    NODE_ID                                = "$${node.unique.id}"
    PORT                                   = "$${NOMAD_PORT_api}"
    POSTGRES_CONNECTION_STRING             = var.postgres_connection_string
    AUTH_DB_CONNECTION_STRING              = var.auth_db_connection_string
    AUTH_DB_READ_REPLICA_CONNECTION_STRING = var.auth_db_read_replica_connection_string
    CLICKHOUSE_CONNECTION_STRING           = var.clickhouse_connection_string
    SUPABASE_JWT_SECRETS                   = var.supabase_jwt_secrets
    OTEL_COLLECTOR_GRPC_ENDPOINT           = "localhost:${var.otel_collector_grpc_port}"
    LOGS_COLLECTOR_ADDRESS                 = "http://localhost:${var.logs_proxy_port.port}"
  }

  extra_env = {
    for key, value in var.extra_env : key => value
    if value != null && trimspace(value) != ""
  }

  conflicting_extra_env_keys = sort(tolist(setintersection(
    toset(keys(local.base_env)),
    toset(keys(local.extra_env)),
  )))

  env = merge(local.base_env, local.extra_env)
}

resource "nomad_job" "dashboard_api" {
  jobspec = templatefile("${path.module}/jobs/dashboard-api.hcl", {
    update_stanza = var.update_stanza
    node_pool     = var.node_pool
    image_name    = var.image

    count = var.count_instances

    memory_mb = 512
    cpu_count = 1

    env = local.env

    subdomain = "dashboard-api"
  })

  lifecycle {
    precondition {
      condition     = length(local.conflicting_extra_env_keys) == 0
      error_message = "dashboard-api extra_env contains reserved keys: ${join(", ", local.conflicting_extra_env_keys)}"
    }
  }
}
