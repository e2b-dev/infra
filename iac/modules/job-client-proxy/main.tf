locals {
  api_internal_grpc_address = trimspace(var.api_internal_grpc_address)
  api_edge_grpc_address     = trimspace(var.api_edge_grpc_address)
}

resource "nomad_job" "client_proxy" {
  jobspec = templatefile("${path.module}/jobs/client-proxy.hcl", {
    update_stanza       = var.update_stanza
    count               = var.client_proxy_count
    cpu_count           = var.client_proxy_cpu_count
    memory_mb           = var.client_proxy_memory_mb
    update_max_parallel = var.client_proxy_update_max_parallel

    node_pool   = var.node_pool
    environment = var.environment

    proxy_port  = var.proxy_port
    health_port = var.health_port

    redis_url           = var.redis_url
    redis_cluster_url   = var.redis_cluster_url
    redis_tls_ca_base64 = var.redis_tls_ca_base64
    redis_pool_size     = var.redis_pool_size

    image                     = var.image
    api_internal_grpc_address = local.api_internal_grpc_address
    api_edge_grpc_address     = local.api_edge_grpc_address

    api_edge_grpc_oauth_client_id     = trimspace(var.api_edge_grpc_oauth_client_id)
    api_edge_grpc_oauth_client_secret = trimspace(var.api_edge_grpc_oauth_client_secret)
    api_edge_grpc_oauth_token_url     = trimspace(var.api_edge_grpc_oauth_token_url)

    otel_collector_grpc_endpoint = var.otel_collector_grpc_endpoint
    logs_collector_address       = var.logs_collector_address
    launch_darkly_api_key        = trimspace(var.launch_darkly_api_key)
  })
}
