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

variable "count_instances" {
  type = number
}

variable "postgres_connection_string" {
  type      = string
  sensitive = true
}

variable "admin_token" {
  type      = string
  sensitive = true
}

variable "auth_db_connection_string" {
  type      = string
  sensitive = true
}

variable "auth_db_read_replica_connection_string" {
  type      = string
  sensitive = true
  default   = ""
}

variable "supabase_db_connection_string" {
  type      = string
  sensitive = true
  default   = ""
}

variable "clickhouse_connection_string" {
  type      = string
  sensitive = true
}

variable "supabase_jwt_secrets" {
  type      = string
  sensitive = true
}

variable "oauth_jwks_url" {
  type    = string
  default = ""
}

variable "oauth_issuer" {
  type    = string
  default = ""
}

variable "oauth_audience" {
  type    = string
  default = ""
}

variable "oauth_user_id_claim" {
  type    = string
  default = "sub"
}

variable "oauth_email_claim" {
  type    = string
  default = "email"
}

variable "oauth_jwks_cache_duration" {
  type    = string
  default = "5m"
}

variable "enable_auth_user_sync_background_worker" {
  type    = bool
  default = false
}

variable "enable_billing_http_team_provision_sink" {
  type    = bool
  default = false
}

variable "otel_collector_grpc_port" {
  type    = number
  default = 4317
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
  default   = ""
}

variable "billing_server_url" {
  type    = string
  default = ""
}

variable "billing_server_api_token" {
  type      = string
  sensitive = true
  default   = ""
}

variable "logs_proxy_port" {
  type = object({
    name = string
    port = number
  })
}
