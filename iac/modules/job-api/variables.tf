variable "node_pool" {
  type = string
}

variable "update_stanza" {
  type = bool
}

variable "prevent_colocation" {
  type    = bool
  default = false
}

variable "count_instances" {
  type = number
}

variable "memory_mb" {
  type = number
}

variable "cpu_count" {
  type = number
}

variable "port_name" {
  type = string
}

variable "port_number" {
  type = number
}

variable "api_internal_grpc_port" {
  type    = number
  default = 5009
}

variable "domain_name" {
  type = string
}

variable "environment" {
  type = string
}

variable "orchestrator_port" {
  type = number
}

variable "nomad_address" {
  type = string
}

variable "api_docker_image" {
  type = string
}

variable "db_migrator_docker_image" {
  type = string
}

variable "postgres_connection_string" {
  type      = string
  sensitive = true
}

variable "postgres_read_replica_connection_string" {
  type      = string
  default   = ""
  sensitive = true
}

variable "supabase_jwt_secrets" {
  type      = string
  sensitive = true
}

variable "posthog_api_key" {
  type      = string
  default   = ""
  sensitive = true
}

variable "analytics_collector_host" {
  type    = string
  default = ""
}

variable "analytics_collector_api_token" {
  type      = string
  default   = ""
  sensitive = true
}

variable "nomad_acl_token" {
  type      = string
  sensitive = true
}

variable "admin_token" {
  type      = string
  sensitive = true
}

variable "sandbox_access_token_hash_seed" {
  type      = string
  sensitive = true
}

variable "sandbox_storage_backend" {
  type    = string
  default = "memory"
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
  default   = ""
  sensitive = true
}

variable "db_max_open_connections" {
  type    = number
  default = 40
}

variable "db_min_idle_connections" {
  type    = number
  default = 5
}

variable "auth_db_max_open_connections" {
  type    = number
  default = 20
}

variable "auth_db_min_idle_connections" {
  type    = number
  default = 5
}

variable "redis_pool_size" {
  type    = number
  default = 160
}

variable "clickhouse_connection_string" {
  type      = string
  default   = ""
  sensitive = true
}

variable "loki_url" {
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

variable "default_persistent_volume_type" {
  type    = string
  default = ""
}

variable "job_env_vars" {
  type    = map(string)
  default = {}
}

variable "grpc_api_http2_tls_enabled" {
  type        = bool
  description = "Expose grpc-api on the HTTP/2 TLS entrypoint."
  default     = false
}

variable "grpc_api_http2_mtls_enabled" {
  type        = bool
  description = "Require the GCP load balancer client certificate on the grpc-api router when it is reached through the HTTP/2 TLS entrypoint."
  default     = false
}

variable "consul_connect_enabled" {
  type        = bool
  description = "Expose api-internal-grpc through a Consul service mesh sidecar."
  default     = false
}

variable "connect_rollout_dependencies" {
  type        = list(string)
  description = "Opaque dependency IDs that must exist before this Connect-enabled job rolls."
  default     = []
}

variable "clickhouse_connect_enabled" {
  type        = bool
  description = "Route API ClickHouse traffic through a Consul Connect upstream."
  default     = false
}

variable "clickhouse_port" {
  type    = number
  default = 9000
}
