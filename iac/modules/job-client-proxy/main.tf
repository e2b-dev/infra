locals {
  job_env_vars = {
    for key, value in var.job_env_vars : key => trimspace(value)
    if try(trimspace(value), "") != ""
  }

  # Convert exposure_type to Traefik entrypoints
  entrypoints = (
    var.exposure_type == "both" ? "web,internal" :
    var.exposure_type == "private" ? "internal" :
    "web"
  )
}

resource "nomad_job" "client_proxy" {
  jobspec = templatefile("${path.module}/jobs/client-proxy.hcl", {
    update_stanza       = var.update_stanza
    count               = var.client_proxy_count
    cpu_count           = var.client_proxy_cpu_count
    memory_mb           = var.client_proxy_memory_mb
    update_max_parallel = var.client_proxy_update_max_parallel

    node_pool = var.node_pool

    proxy_port  = var.proxy_port
    health_port = var.health_port

    image        = var.image
    job_env_vars = local.job_env_vars
    entrypoints  = local.entrypoints
  })
}
