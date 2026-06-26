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

locals {
  job_env_vars = {
    for key, value in var.job_env_vars : key => trimspace(value)
    if value != null && try(trimspace(value), "") != ""
  }
}

resource "nomad_job" "template_manager" {
  jobspec = templatefile("${path.module}/jobs/template-manager.hcl", {
    update_stanza = var.update_stanza
    node_pool     = var.node_pool
    current_count = tonumber(data.external.template_manager_count.result.count)

    port            = var.port
    artifact_source = var.artifact_source
    job_env_vars    = local.job_env_vars
  })
}
