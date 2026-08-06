locals {
  traefik_config = templatefile("${path.module}/jobs/traefik.toml", {
    control_port          = var.control_port
    ingress_port          = var.ingress_port
    ingress_internal_port = var.ingress_internal_port

    nomad_endpoint = var.nomad_endpoint
    nomad_token    = var.nomad_token

    consul_endpoint = var.consul_endpoint
    consul_token    = var.consul_token

    otel_collector_grpc_endpoint = var.otel_collector_grpc_endpoint
  })
}

resource "nomad_job" "ingress" {
  jobspec = templatefile("${path.module}/jobs/ingress.hcl", {
    count               = var.ingress_count
    node_pool           = var.node_pool
    update_stanza       = var.update_stanza
    update_canary_count = var.available_host_count > var.ingress_count ? 1 : 0
    cpu_count           = var.ingress_cpu_count
    memory_mb           = var.ingress_memory_mb

    control_port          = var.control_port
    ingress_port          = var.ingress_port
    ingress_internal_port = var.ingress_internal_port

    traefik_config = local.traefik_config
    config_files   = var.traefik_config_files
  })

  lifecycle {
    precondition {
      condition = (
        var.available_host_count >= 1
        && floor(var.available_host_count) == var.available_host_count
        && var.ingress_count >= 1
        && floor(var.ingress_count) == var.ingress_count
        && var.ingress_count <= var.available_host_count
      )
      error_message = "ingress requires one distinct static-port allocation per eligible API host; replica count must be a positive integer no greater than available_host_count."
    }
  }
}
