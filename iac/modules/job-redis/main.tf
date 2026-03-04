resource "nomad_job" "redis" {
  jobspec = templatefile("${path.module}/jobs/redis.hcl", {
    node_pool   = var.node_pool
    port_number = var.port_number
    port_name   = var.port_name
  })
}
