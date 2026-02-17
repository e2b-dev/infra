variable "vector_api_port" {
  type = number
}

variable "vector_health_port" {
  type = number
}

variable "loki_endpoint" {
  type        = string
  description = "The URL of the Loki instance to which logs will be sent. For example: http://loki:3100"
}

variable "grafana_logs_user" {
  type    = string
  default = "" // Optional
}

variable "grafana_logs_endpoint" {
  type    = string
  default = "" // Optional
}

variable "grafana_api_key" {
  type      = string
  default   = "" // Optional
  sensitive = true
}

variable "vector_config_override" {
  type        = string
  default     = ""
  description = "Custom Vector TOML config. When set, replaces the default config entirely."
}
