# Get current template-manager count from Nomad to preserve autoscaler-managed value
# This prevents Terraform from resetting count on job updates
# Fixed one-worker deployments use a count of one without querying Nomad.
data "external" "template_manager_count" {
  # A single fixed build worker has no autoscaler-managed count to preserve.
  # Skipping the live Nomad lookup also makes the one-worker topology
  # bootstrapable before the Nomad endpoint exists.
  count = var.update_stanza ? 1 : 0

  program = ["bash", "${path.module}/scripts/get-nomad-job-count.sh"]

  query = {
    nomad_addr  = var.nomad_addr
    nomad_token = var.nomad_token
    job_name    = "template-manager"
    min_count   = "2"
  }
}

locals {
  template_manager_count = var.update_stanza ? tonumber(data.external.template_manager_count[0].result.count) : 1

  job_env_vars = {
    for key, value in var.job_env_vars : key => trimspace(value)
    if value != null && try(trimspace(value), "") != ""
  }
}

resource "nomad_job" "template_manager" {
  jobspec = templatefile("${path.module}/jobs/template-manager.hcl", {
    update_stanza = var.update_stanza
    node_pool     = var.node_pool
    current_count = local.template_manager_count

    port            = var.port
    artifact_source = var.artifact_source
    job_env_vars    = local.job_env_vars
  })
}
