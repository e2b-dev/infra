locals {
  # The mutation switch is derived, never passed through: only the scale-out
  # mode can render the exact double-keyed phrase the binary requires.
  mutation_enabled = var.mode == "scale-out" ? "scale-out-only" : "false"
}

resource "nomad_job" "controller" {
  jobspec = templatefile("${path.module}/jobs/monad-worker-autoscaler.hcl", {
    mode              = var.mode
    mutation_enabled  = local.mutation_enabled
    node_pool         = var.node_pool
    worker_node_pool  = var.worker_node_pool
    allocation_count  = var.allocation_count
    artifact_source   = var.artifact_source
    tams_capacity_url = var.tams_capacity_url
    tams_audience     = var.tams_audience
    nomad_token       = var.nomad_token
    consul_token      = var.consul_token
    metrics_port      = var.metrics_port
    mig_project_id    = var.mig_project_id
    mig_region        = var.mig_region
    mig_name          = var.mig_name
    worker_host_floor = var.worker_host_floor
  })

  lifecycle {
    precondition {
      condition     = var.tams_audience == trimsuffix(var.tams_capacity_url, "/v1/ops/capacity")
      error_message = "The TAMS identity-token audience must be the exact HTTPS origin of the capacity endpoint."
    }

    precondition {
      condition = (
        var.worker_node_pool == "default"
        && length(var.worker_cluster_keys) == 1
        && toset(var.worker_cluster_keys) == toset(["default"])
        && var.worker_cluster_size == var.worker_host_floor
        && var.worker_machine_type == "n1-standard-8"
      )
      error_message = "The invited-beta controller requires one isolated n1-standard-8 Terraform client cluster named default in the Nomad default node pool, sized exactly at the reviewed worker-host floor."
    }

    precondition {
      condition     = var.mode == "shadow" || (var.mig_project_id != "" && var.mig_region != "" && var.mig_name != "")
      error_message = "scale-out mode requires mig_project_id, mig_region, and mig_name."
    }

    precondition {
      condition     = var.mode == "scale-out" || (var.mig_project_id == "" && var.mig_region == "" && var.mig_name == "")
      error_message = "shadow mode must not configure a resize target."
    }
  }
}
