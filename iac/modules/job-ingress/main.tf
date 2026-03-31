locals {
  traefik_config = templatefile("${path.module}/jobs/traefik.toml", {
    ingress_port    = var.ingress_proxy_port
    control_port    = var.ingress_control_port
    log_level       = var.traefik_log_level

    nomad_endpoint = var.nomad_endpoint
    nomad_token    = var.nomad_token

    consul_endpoint = var.consul_endpoint
    consul_token    = var.consul_token

    otel_collector_grpc_endpoint = var.otel_collector_grpc_endpoint
  })
}

resource "nomad_job" "ingress" {
  jobspec = templatefile("${path.module}/jobs/ingress.hcl", {
    count         = var.ingress_count
    node_pool     = var.node_pool
    update_stanza = var.update_stanza
    cpu_count     = var.ingress_cpu_count
    memory_mb     = var.ingress_memory_mb

    ingress_port    = var.ingress_proxy_port
    control_port    = var.ingress_control_port
    additional_args = var.additional_traefik_arguments

    traefik_config = local.traefik_config
    config_files   = var.traefik_config_files
  })
}
