locals {
  job_env_vars = {
    for key, value in var.job_env_vars : key => trimspace(value)
    if try(trimspace(value), "") != ""
  }

  db_migrator_env_vars = {
    for key, value in var.db_migrator_env_vars : key => trimspace(value)
    if try(trimspace(value), "") != ""
  }
}

resource "nomad_job" "api" {
  jobspec = templatefile("${path.module}/jobs/api.hcl", {
    update_stanza      = var.update_stanza
    node_pool          = var.node_pool
    prevent_colocation = var.prevent_colocation
    count              = var.count_instances

    memory_mb = var.memory_mb
    cpu_count = var.cpu_count

    port_name                = var.port_name
    port_number              = var.port_number
    api_internal_grpc_port   = var.api_internal_grpc_port
    api_docker_image         = var.api_docker_image
    db_migrator_docker_image = var.db_migrator_docker_image
    job_env_vars             = local.job_env_vars
    db_migrator_env_vars     = local.db_migrator_env_vars
  })
}
