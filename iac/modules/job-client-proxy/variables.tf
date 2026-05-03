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

variable "proxy_tls_port" {
  type    = number
  default = 3004
}

variable "health_port" {
  type    = number
  default = 3001
}

variable "tls_cert_pem" {
  type      = string
  default   = ""
  sensitive = true
}

variable "tls_key_pem" {
  type      = string
  default   = ""
  sensitive = true
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

variable "redis_pool_size" {
  type    = number
  default = 40
}

variable "image" {
  type = string
}

variable "api_internal_grpc_address" {
  type    = string
  default = ""
}

variable "api_edge_grpc_address" {
  type    = string
  default = ""
}

variable "api_edge_grpc_oauth_client_id" {
  type      = string
  default   = ""
  sensitive = true
}

variable "api_edge_grpc_oauth_client_secret" {
  type      = string
  default   = ""
  sensitive = true
}

variable "api_edge_grpc_oauth_token_url" {
  type      = string
  default   = ""
  sensitive = true
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
