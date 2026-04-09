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
    REDIS_URL                              = var.redis_url
    REDIS_CLUSTER_URL                      = var.redis_cluster_url
    REDIS_TLS_CA_BASE64                    = var.redis_tls_ca_base64
    BILLING_SERVER_URL                     = var.billing_server_url
    BILLING_SERVER_API_TOKEN               = var.billing_server_api_token
    OTEL_COLLECTOR_GRPC_ENDPOINT           = "localhost:${var.otel_collector_grpc_port}"
    LOGS_COLLECTOR_ADDRESS                 = "http://localhost:${var.logs_proxy_port.port}"
  }

  env = merge(local.base_env, var.extra_env)
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
}
