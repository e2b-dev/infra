locals {
  orchestrator_vars = {
    node_pool  = var.node_pool
    port       = var.port
    proxy_port = var.proxy_port

    environment                  = var.environment
    logs_collector_address       = var.logs_collector_address
    otel_collector_grpc_endpoint = var.otel_collector_grpc_endpoint
    envd_timeout                 = var.envd_timeout
    template_bucket_name         = var.template_bucket_name
    allow_sandbox_internet       = var.allow_sandbox_internet
    clickhouse_connection_string = var.clickhouse_connection_string
    redis_url                    = var.redis_url
    redis_cluster_url            = var.redis_cluster_url
    redis_tls_ca_base64          = var.redis_tls_ca_base64

    consul_token            = var.consul_token
    domain_name             = var.domain_name
    shared_chunk_cache_path = var.shared_chunk_cache_path
    launch_darkly_api_key   = var.launch_darkly_api_key
    orchestrator_services   = var.orchestrator_services
    build_cache_bucket_name = var.build_cache_bucket_name

    provider            = var.provider_name
    provider_aws_config = var.provider_aws_config

    artifact_source = var.artifact_source

    use_local_namespace_storage = var.use_local_namespace_storage
  }

  # Render with placeholder to detect changes in job definition
  orchestrator_job_check = templatefile("${path.module}/jobs/orchestrator.hcl", merge(
    local.orchestrator_vars, {
      latest_orchestrator_job_id = "placeholder",
    }
  ))
}

resource "random_id" "orchestrator_job" {
  keepers = {
    # Use both the orchestrator job (including vars) definition and the latest orchestrator checksum to detect changes
    orchestrator_job = sha256("${local.orchestrator_job_check}-${var.orchestrator_checksum}")
  }

  byte_length = 8
}

locals {
  latest_orchestrator_job_id = var.environment == "dev" ? "dev" : random_id.orchestrator_job.hex
}

resource "nomad_variable" "orchestrator_hash" {
  path = "nomad/jobs"
  items = {
    latest_orchestrator_job_id = local.latest_orchestrator_job_id
  }
}

resource "nomad_job" "orchestrator" {
  deregister_on_id_change = false

  jobspec = templatefile("${path.module}/jobs/orchestrator.hcl", merge(
    local.orchestrator_vars, {
      latest_orchestrator_job_id = local.latest_orchestrator_job_id
    }
  ))

  depends_on = [nomad_variable.orchestrator_hash, random_id.orchestrator_job]
}
