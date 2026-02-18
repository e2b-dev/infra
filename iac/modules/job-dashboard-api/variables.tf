variable "node_pool" {
  type = string
}

variable "update_stanza" {
  type = bool
}

variable "environment" {
  type = string
}

variable "image" {
  type = string
}

variable "dashboard_api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "postgres_connection_string" {
  type      = string
  sensitive = true
}

variable "clickhouse_connection_string" {
  type      = string
  sensitive = true
}

variable "otel_collector_grpc_port" {
  type    = number
  default = 4317
}

variable "logs_proxy_port" {
  type = object({
    name = string
    port = number
  })
}
