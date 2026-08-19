resource "nomad_job" "shadow" {
  jobspec = templatefile("${path.module}/jobs/monad-worker-autoscaler.hcl", {
    node_pool         = var.node_pool
    worker_node_pool  = var.worker_node_pool
    allocation_count  = var.allocation_count
    artifact_source   = var.artifact_source
    tams_capacity_url = var.tams_capacity_url
    tams_audience     = var.tams_audience
    nomad_token       = var.nomad_token
    consul_token      = var.consul_token
    metrics_port      = var.metrics_port
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
        && var.worker_cluster_size == 6
        && var.worker_machine_type == "n1-standard-8"
      )
      error_message = "The invited-beta observer requires one isolated six-host n1-standard-8 Terraform client cluster named default in the Nomad default node pool."
    }
  }
}
