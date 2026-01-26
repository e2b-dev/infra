locals {
  clickhouse_connection_string = var.clickhouse_server_count > 0 ? "clickhouse://${var.clickhouse_username}:${random_password.clickhouse_password.result}@clickhouse.service.consul:${var.clickhouse_server_port.port}/${var.clickhouse_database}" : ""
}

resource "random_password" "clickhouse_password" {
  length  = 24
  special = false
}

resource "null_resource" "postgres_change_trigger" {
  triggers = {
    hash = sha1(var.postgres_connection_string)
  }
}

resource "nomad_job" "ingress" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/ingress.hcl",
    {
      count         = var.ingress_count
      update_stanza = var.api_machine_count > 1
      cpu_count     = 1
      memory_mb     = 512
      node_pool     = var.api_node_pool
      datacenter    = var.datacenter

      ingress_port = var.ingress_port.port
      control_port = 8900

      nomad_endpoint = var.nomad_address
      nomad_token    = var.nomad_acl_token

      consul_token         = var.consul_acl_token
      consul_endpoint      = var.consul_address
      consul_endpoint_host = replace(var.consul_address, "http://", "")
      ingress_node_ip      = var.ingress_node_ip
      docker_image_prefix  = var.docker_image_prefix
  })
}

resource "nomad_job" "api" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/api.hcl", {
    count              = var.api_machine_count
    update_stanza      = var.api_machine_count > 1
    node_pool          = var.api_node_pool
    prevent_colocation = var.api_machine_count > 2

    memory_mb = var.api_resources_memory_mb
    cpu_count = var.api_resources_cpu_count

    orchestrator_port              = var.orchestrator_port
    template_manager_host          = "template-manager.service.consul:${var.template_manager_port}"
    otel_collector_grpc_endpoint   = "localhost:${var.otel_collector_grpc_port}"
    logs_collector_address         = "http://localhost:${var.logs_proxy_port.port}"
    datacenter                     = var.datacenter
    port_name                      = var.api_port.name
    port_number                    = var.api_port.port
    api_docker_image               = var.api_image
    postgres_connection_string     = var.postgres_connection_string
    supabase_jwt_secrets           = var.supabase_jwt_secrets
    posthog_api_key                = var.posthog_api_key
    environment                    = var.environment
    analytics_collector_host       = var.analytics_collector_host
    analytics_collector_api_token  = var.analytics_collector_api_token
    otel_tracing_print             = var.otel_tracing_print
    nomad_acl_token                = var.nomad_acl_token
    admin_token                    = var.api_admin_token
    redis_url                      = var.redis_url
    redis_secure_cluster_url       = var.redis_secure_cluster_url
    dns_port_number                = 5353
    clickhouse_connection_string   = local.clickhouse_connection_string
    db_migrator_docker_image       = var.db_migrator_image
    launch_darkly_api_key          = trimspace(var.launch_darkly_api_key)
    sandbox_access_token_hash_seed = var.sandbox_access_token_hash_seed

    local_cluster_endpoint = "edge-api.service.consul:${var.edge_api_port.port}"
    local_cluster_token    = var.edge_api_secret
    domain_name            = var.domain_name
  })

  lifecycle {
    replace_triggered_by = [null_resource.postgres_change_trigger]
  }
}

resource "nomad_job" "client_proxy" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/edge.hcl",
    {
      update_stanza       = var.api_machine_count > 1
      count               = 1
      cpu_count           = var.api_resources_cpu_count
      memory_mb           = var.api_resources_memory_mb
      update_max_parallel = 1

      node_pool = var.api_node_pool

      datacenter  = var.datacenter
      environment = var.environment

      redis_url         = var.redis_url
      redis_cluster_url = var.redis_secure_cluster_url

      loki_url = "http://loki.service.consul:${var.loki_service_port.port}"

      proxy_port_name   = var.edge_proxy_port.name
      proxy_port        = var.edge_proxy_port.port
      api_port_name     = var.edge_api_port.name
      api_port          = var.edge_api_port.port
      api_secret        = var.edge_api_secret
      orchestrator_port = var.template_manager_port
      domain_name       = var.domain_name

      image_name = var.client_proxy_image

      nomad_endpoint = "http://localhost:4646"
      nomad_token    = var.nomad_acl_token

      otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
      logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
      launch_darkly_api_key        = trimspace(var.launch_darkly_api_key)
  })
}

resource "nomad_job" "redis" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/redis.hcl",
    {
      node_pool           = var.api_node_pool
      datacenter          = var.datacenter
      port_number         = 6379
      port_name           = "redis"
      docker_image_prefix = var.docker_image_prefix
    }
  )
}

resource "nomad_job" "docker_reverse_proxy" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/docker-reverse-proxy.hcl",
    {
      datacenter                    = var.datacenter
      node_pool                     = var.api_node_pool
      image_name                    = var.docker_reverse_proxy_image
      postgres_connection_string    = var.postgres_connection_string
      google_service_account_secret = ""
      port_number                   = 30007
      port_name                     = "docker-reverse-proxy"
      health_check_path             = "/health"
      domain_name                   = var.domain_name
      gcp_project_id                = ""
      gcp_region                    = ""
      docker_registry               = ""
      docker_image_prefix           = var.docker_image_prefix
    }
  )

  lifecycle {
    replace_triggered_by = [null_resource.postgres_change_trigger]
  }
}

resource "nomad_job" "otel_collector" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/otel-collector.hcl", {
    memory_mb  = var.otel_collector_resources_memory_mb
    cpu_count  = var.otel_collector_resources_cpu_count
    datacenter = var.datacenter

    node_pool                = var.api_node_pool
    otel_collector_grpc_port = var.otel_collector_grpc_port
    otel_collector_config = templatefile("${path.module}/configs/otel-collector.yaml", {
      otel_collector_grpc_port = var.otel_collector_grpc_port
      loki_endpoint            = "http://localhost:${var.loki_service_port.port}"
    })
    docker_image_prefix = var.docker_image_prefix
  })
}

resource "nomad_job" "logs_collector" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/logs-collector.hcl", {
    logs_health_port_number  = var.logs_health_proxy_port.port
    logs_port_number         = var.logs_proxy_port.port
    logs_health_path         = var.logs_health_proxy_port.health_path
    loki_service_port_number = var.loki_service_port.port
    docker_image_prefix      = var.docker_image_prefix
  })
}

resource "nomad_job" "loki" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/loki.hcl", {
    datacenter               = var.datacenter
    node_pool                = var.api_node_pool
    memory_mb                = var.loki_resources_memory_mb
    cpu_count                = var.loki_resources_cpu_count
    loki_service_port_number = var.loki_service_port.port
    loki_service_port_name   = var.loki_service_port.name
    docker_image_prefix      = var.docker_image_prefix
  })
}

resource "nomad_job" "grafana" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/grafana.hcl", {
    datacenter                  = var.datacenter
    node_pool                   = var.api_node_pool
    memory_mb                   = var.grafana_resources_memory_mb
    cpu_count                   = var.grafana_resources_cpu_count
    grafana_service_port_number = var.grafana_service_port.port
    grafana_service_port_name   = var.grafana_service_port.name
    loki_service_port_number    = var.loki_service_port.port
    docker_image_prefix         = var.docker_image_prefix
  })
}

resource "nomad_job" "template_manager" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/template-manager.hcl", {
    update_stanza                        = var.template_manager_machine_count > 1
    node_pool                            = var.builder_node_pool
    datacenter                           = var.datacenter
    port                                 = var.template_manager_port
    proxy_port                           = var.orchestrator_proxy_port
    environment                          = var.environment
    consul_acl_token                     = var.consul_acl_token
    api_secret                           = var.api_secret
    artifact_url                         = var.template_manager_artifact_url
    envd_artifact_url                    = var.envd_artifact_url
    should_download_envd                 = contains(var.fc_artifact_node_pools, var.builder_node_pool)
    template_manager_checksum            = ""
    otel_tracing_print                   = var.otel_tracing_print
    template_bucket_name                 = var.template_bucket_name
    build_cache_bucket_name              = var.build_cache_bucket_name
    otel_collector_grpc_endpoint         = "localhost:${var.otel_collector_grpc_port}"
    logs_collector_address               = "http://localhost:${var.logs_proxy_port.port}"
    orchestrator_services                = "template-manager"
    clickhouse_connection_string         = local.clickhouse_connection_string
    dockerhub_remote_repository_url      = var.dockerhub_remote_repository_url
    dockerhub_remote_repository_provider = var.dockerhub_remote_repository_provider
    launch_darkly_api_key                = trimspace(var.launch_darkly_api_key)
    shared_chunk_cache_path              = var.shared_chunk_cache_path
    sandbox_hyperloop_proxy_port         = var.sandbox_hyperloop_proxy_port
    use_local_namespace_storage          = var.use_local_namespace_storage
    use_nfs_share_storage                = var.use_nfs_share_storage
    nfs_server_ip                        = var.nfs_server_ip
    start_script = templatefile("${path.module}/scripts/template_manager_start.sh.tpl", {
      use_nfs_share_storage = var.use_nfs_share_storage
      nfs_server_ip         = var.nfs_server_ip
    })
  })
}

resource "nomad_job" "orchestrator" {
  count = var.enable_nomad_jobs ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/orchestrator.hcl", {
    node_pool                            = var.orchestrator_node_pool
    port                                 = var.orchestrator_port
    proxy_port                           = var.orchestrator_proxy_port
    environment                          = var.environment
    consul_acl_token                     = var.consul_acl_token
    otel_tracing_print                   = var.otel_tracing_print
    logs_collector_address               = "http://localhost:${var.logs_proxy_port.port}"
    envd_timeout                         = var.envd_timeout
    template_bucket_name                 = var.template_bucket_name
    otel_collector_grpc_endpoint         = "localhost:${var.otel_collector_grpc_port}"
    allow_sandbox_internet               = var.allow_sandbox_internet
    shared_chunk_cache_path              = var.shared_chunk_cache_path
    clickhouse_connection_string         = local.clickhouse_connection_string
    redis_url                            = var.redis_url
    redis_cluster_url                    = var.redis_secure_cluster_url
    redis_tls_ca_base64                  = var.redis_tls_ca_base64
    launch_darkly_api_key                = trimspace(var.launch_darkly_api_key)
    artifact_url                         = var.orchestrator_artifact_url
    use_local_namespace_storage          = var.use_local_namespace_storage
    dockerhub_remote_repository_url      = var.dockerhub_remote_repository_url
    dockerhub_remote_repository_provider = var.dockerhub_remote_repository_provider
    use_nfs_share_storage                = var.use_nfs_share_storage
    nfs_server_ip                        = var.nfs_server_ip
    start_script = templatefile("${path.module}/scripts/orchestrator_start.sh.tpl", {
      use_nfs_share_storage = var.use_nfs_share_storage
      nfs_server_ip         = var.nfs_server_ip
    })
  })
}

resource "nomad_job" "clickhouse" {
  count = var.enable_nomad_jobs && (var.clickhouse_server_count > 0) ? 1 : 0
  jobspec = templatefile("${path.module}/jobs/clickhouse.hcl", {
    server_count            = var.clickhouse_server_count
    node_pool               = var.api_node_pool
    username                = var.clickhouse_username
    cpu_count               = var.clickhouse_resources_cpu_count
    memory_mb               = var.clickhouse_resources_memory_mb
    clickhouse_server_port  = var.clickhouse_server_port.port
    clickhouse_metrics_port = var.clickhouse_metrics_port
    clickhouse_version      = "24.3.3"
    docker_image_prefix     = var.docker_image_prefix
  })
}
