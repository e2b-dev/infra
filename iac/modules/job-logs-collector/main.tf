locals {
  default_vector_config = templatefile(
    "${path.module}/configs/vector.toml", {
      vector_api_port    = var.vector_api_port
      vector_health_port = var.vector_health_port

      loki_endpoint = var.loki_endpoint

      grafana_logs_user     = var.grafana_logs_user
      grafana_logs_endpoint = var.grafana_logs_endpoint
      grafana_api_key       = var.grafana_api_key
    },
  )

  // Allow config override for flexibility
  vector_config = var.vector_config_override != "" ? var.vector_config_override : local.default_vector_config
}

resource "nomad_job" "logs_collector" {
  jobspec = templatefile("${path.module}/jobs/logs-collector.hcl", {
    vector_api_port    = var.vector_api_port
    vector_health_port = var.vector_health_port
    vector_config      = local.vector_config
  })
}
