locals {
  job_env_vars = {
    for key, value in var.job_env_vars : key => trimspace(value)
    if try(trimspace(value), "") != ""
  }
}

resource "nomad_job" "dashboard_api" {
  jobspec = templatefile("${path.module}/jobs/dashboard-api.hcl", {
    update_stanza = var.update_stanza
    node_pool     = var.node_pool
    image_name    = var.image

    count = var.count_instances

    memory_mb = 512
    cpu_count = 1

    job_env_vars = local.job_env_vars

    subdomain = "dashboard-api"
  })
}
