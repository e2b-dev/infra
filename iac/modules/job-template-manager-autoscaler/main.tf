resource "nomad_job" "nomad_nodepool_apm" {
  jobspec = templatefile("${path.module}/jobs/nomad-autoscaler.hcl", {
    node_pool                  = var.node_pool
    autoscaler_version         = var.autoscaler_version
    nomad_token                = var.nomad_token
    apm_plugin_artifact_source = var.apm_plugin_artifact_source
    apm_plugin_checksum        = var.apm_plugin_checksum
  })
}
