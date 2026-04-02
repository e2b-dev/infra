resource "nomad_job" "dashboard_api" {
  jobspec = templatefile("${path.module}/jobs/dashboard-api.hcl", {
    update_stanza = var.update_stanza
    node_pool     = var.node_pool
    image_name    = var.image
    environment   = var.environment

    count = var.count_instances

    memory_mb = 512
    cpu_count = 1

    postgres_connection_string             = var.postgres_connection_string
    auth_db_connection_string              = var.auth_db_connection_string
    auth_db_read_replica_connection_string = var.auth_db_read_replica_connection_string
    clickhouse_connection_string           = var.clickhouse_connection_string
    supabase_jwt_secrets                   = var.supabase_jwt_secrets
    redis_url                              = var.redis_url
    redis_cluster_url                      = var.redis_cluster_url
    redis_tls_ca_base64                    = var.redis_tls_ca_base64

    subdomain = "dashboard-api"

    otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
    logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
  })
}
