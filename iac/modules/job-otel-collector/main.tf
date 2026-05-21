locals {
  default_otel_collector_config = templatefile(
    "${path.module}/configs/otel-collector.yaml", {
      provider_name                = var.provider_name
      grafana_otel_collector_token = var.grafana_otel_collector_token
      grafana_otlp_url             = var.grafana_otlp_url
      grafana_username             = var.grafana_username
      consul_token                 = var.consul_token

      clickhouse_username = var.clickhouse_username
      clickhouse_password = var.clickhouse_password
      clickhouse_port     = var.clickhouse_port
      clickhouse_host     = var.clickhouse_host
      clickhouse_database = var.clickhouse_database

      enable_otel_router_metrics = var.enable_otel_router_metrics
      otel_router_grpc_port      = var.otel_router_grpc_port

      enable_gcp_telemetry_metrics          = var.enable_gcp_telemetry_metrics
      enable_gcp_telemetry_external_metrics = var.enable_gcp_telemetry_external_metrics
      gcp_telemetry_project_id              = var.gcp_telemetry_project_id
    },
  )

  // Allow config override for flexibility
  otel_collector_config = var.otel_collector_config_override != "" ? var.otel_collector_config_override : local.default_otel_collector_config
}

resource "nomad_job" "otel_collector" {
  jobspec = templatefile("${path.module}/jobs/otel-collector.hcl", {
    memory_mb = var.memory_mb
    cpu_count = var.cpu_count

    otel_collector_grpc_port = var.otel_collector_grpc_port
    otel_collector_config    = local.otel_collector_config
  })

  lifecycle {
    precondition {
      condition     = !var.enable_gcp_telemetry_metrics || var.gcp_telemetry_project_id != ""
      error_message = "gcp_telemetry_project_id must be set when enable_gcp_telemetry_metrics is true."
    }

    precondition {
      condition     = !var.enable_gcp_telemetry_external_metrics || var.enable_gcp_telemetry_metrics
      error_message = "enable_gcp_telemetry_metrics must be true when enable_gcp_telemetry_external_metrics is true."
    }
  }
}

variable "provider_name" {
  type        = string
  description = "Cloud provider: gcp or aws"

  validation {
    condition     = contains(["gcp", "aws"], var.provider_name)
    error_message = "provider_name must be 'gcp' or 'aws'"
  }
}

variable "memory_mb" {
  type    = number
  default = 512
}

variable "cpu_count" {
  type    = number
  default = 1
}

variable "otel_collector_grpc_port" {
  type = number
}

variable "grafana_otel_collector_token" {
  type        = string
  sensitive   = true
  description = "Grafana Cloud OTel collector token. Required for default config, pass dummy value if using otel_collector_config_override."
}

variable "grafana_otlp_url" {
  type        = string
  description = "Grafana Cloud OTLP URL. Required for default config, pass dummy value if using otel_collector_config_override."
}

variable "grafana_username" {
  type        = string
  description = "Grafana Cloud username. Required for default config, pass dummy value if using otel_collector_config_override."
}

variable "consul_token" {
  type      = string
  default   = ""
  sensitive = true
}

variable "clickhouse_username" {
  type    = string
  default = ""
}

variable "clickhouse_password" {
  type      = string
  default   = ""
  sensitive = true
}

variable "clickhouse_port" {
  type    = number
  default = 9000
}

variable "clickhouse_host" {
  type    = string
  default = "clickhouse.service.consul"
}

variable "clickhouse_database" {
  type    = string
  default = ""
}

variable "enable_otel_router_metrics" {
  type        = bool
  default     = false
  description = "Enable teeing external customer metrics from otel-collector to otel-router."
}

variable "otel_router_grpc_port" {
  type        = number
  default     = 4320
  description = "Local otel-router OTLP gRPC port used by otel-collector when otel-router metric teeing is enabled."
}

variable "otel_collector_config_override" {
  type        = string
  default     = ""
  description = "Custom OTel collector YAML config. When set, replaces the default config entirely."
}

variable "enable_gcp_telemetry_metrics" {
  type        = bool
  default     = false
  description = "Enable exporting selected metrics to Google Cloud Monitoring using the googlecloud exporter."
}

variable "enable_gcp_telemetry_external_metrics" {
  type        = bool
  default     = false
  description = "Enable exporting external e2b.* metrics to Google Cloud Monitoring. Requires enable_gcp_telemetry_metrics."
}

variable "gcp_telemetry_project_id" {
  type        = string
  default     = ""
  description = "Google Cloud project ID used for native Cloud Monitoring metric export. Required when enable_gcp_telemetry_metrics is true."
}
