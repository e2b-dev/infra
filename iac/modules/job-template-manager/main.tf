# Get current template-manager count from Nomad to preserve autoscaler-managed value
# This prevents Terraform from resetting count on job updates
# Default depends on whether scaling is enabled (min=2) or not (min=1)
data "external" "template_manager_count" {
  program = ["bash", "${path.module}/scripts/get-nomad-job-count.sh"]

  query = {
    nomad_addr  = var.nomad_addr
    nomad_token = var.nomad_token
    job_name    = "template-manager"
    min_count   = var.update_stanza ? "2" : "1"
  }
}

resource "nomad_job" "template_manager" {
  jobspec = templatefile("${path.module}/jobs/template-manager.hcl", {
    update_stanza = var.update_stanza
    node_pool     = var.node_pool
    current_count = tonumber(data.external.template_manager_count.result.count)

    provider            = var.provider_name
    provider_gcp_config = var.provider_gcp_config
    provider_aws_config = var.provider_aws_config

    port             = var.port
    environment      = var.environment
    consul_acl_token = var.consul_acl_token
    domain_name      = var.domain_name

    api_secret                      = var.api_secret
    artifact_source                 = var.artifact_source
    template_manager_checksum       = var.template_manager_checksum
    template_bucket_name            = var.template_bucket_name
    build_cache_bucket_name         = var.build_cache_bucket_name
    otel_collector_grpc_endpoint    = var.otel_collector_grpc_endpoint
    logs_collector_address          = var.logs_collector_address
    orchestrator_services           = var.orchestrator_services
    clickhouse_connection_string    = var.clickhouse_connection_string
    dockerhub_remote_repository_url = var.dockerhub_remote_repository_url
    redis_pool_size                 = var.redis_pool_size
    launch_darkly_api_key           = trimspace(var.launch_darkly_api_key)
    shared_chunk_cache_path         = var.shared_chunk_cache_path
  })
}
