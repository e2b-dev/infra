variable "update_stanza" {
  type = bool
}

variable "client_proxy_count" {
  type = number
}

variable "client_proxy_cpu_count" {
  type    = number
  default = 1
}

variable "client_proxy_memory_mb" {
  type    = number
  default = 512
}

variable "client_proxy_update_max_parallel" {
  type    = number
  default = 1
}

variable "node_pool" {
  type = string
}

variable "environment" {
  type = string
}

variable "proxy_port" {
  type    = number
  default = 3002
}

variable "health_port" {
  type    = number
  default = 3001
}

variable "redis_url" {
  type      = string
  sensitive = true
}

variable "redis_cluster_url" {
  type      = string
  sensitive = true
}

variable "redis_tls_ca_base64" {
  type      = string
  sensitive = true
}

variable "image" {
  type = string
}

variable "api_grpc_address" {
  type    = string
  default = ""
}

variable "otel_collector_grpc_endpoint" {
  type = string
}

variable "logs_collector_address" {
  type = string
}

variable "launch_darkly_api_key" {
  type      = string
  default   = ""
  sensitive = true
}
