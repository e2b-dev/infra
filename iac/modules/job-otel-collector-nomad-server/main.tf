locals {
  default_otel_collector_config = templatefile(
    "${path.module}/configs/otel-collector-nomad-server.yaml", {
      provider_name                = var.provider_name
      grafana_otel_collector_token = var.grafana_otel_collector_token
      grafana_otlp_url             = var.grafana_otlp_url
      grafana_username             = var.grafana_username
    },
  )

  // Allow config override for flexibility
  otel_collector_config = var.otel_collector_config_override != "" ? var.otel_collector_config_override : local.default_otel_collector_config
}

resource "nomad_job" "otel_collector_nomad_server" {
  jobspec = templatefile("${path.module}/jobs/otel-collector-nomad-server.hcl", {
    node_pool             = var.node_pool
    otel_collector_config = local.otel_collector_config
  })
}

variable "provider_name" {
  type        = string
  description = "Cloud provider: gcp or aws"

  validation {
    condition     = contains(["gcp", "aws"], var.provider_name)
    error_message = "provider_name must be 'gcp' or 'aws'"
  }
}

variable "node_pool" {
  type = string
}

variable "grafana_otel_collector_token" {
  type      = string
  sensitive = true
}

variable "grafana_otlp_url" {
  type = string
}

variable "grafana_username" {
  type = string
}

variable "otel_collector_config_override" {
  type        = string
  default     = ""
  description = "Custom OTel collector YAML config. When set, replaces the default config entirely."
}
