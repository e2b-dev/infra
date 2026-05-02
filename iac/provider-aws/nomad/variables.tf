# Core
variable "domain_name" {
  type = string
}

variable "environment" {
  type = string
}

variable "aws_region" {
  type = string
}

# Auth
variable "nomad_acl_token" {
  type      = string
  sensitive = true
}

variable "consul_acl_token" {
  type      = string
  sensitive = true
}

# Node pools
variable "api_node_pool" {
  type = string
}

variable "clickhouse_node_pool" {
  type = string
}

variable "clickhouse_jobs_prefix" {
  type = string
}

# Cluster sizes
variable "api_cluster_size" {
  type = number
}

# Ingress
variable "ingress_port" {
  type = number
}

variable "ingress_count" {
  type = number
}

# Client Proxy
variable "client_proxy_count" {
  type    = number
  default = 1
}

variable "client_proxy_repository_name" {
  type = string
}

# Redis
variable "redis_managed" {
  type = bool
}

variable "redis_port" {
  type = number
}

variable "redis_url" {
  type    = string
  default = ""
}

variable "redis_cluster_url" {
  type      = string
  default   = ""
  sensitive = true
}

variable "redis_tls_ca_base64" {
  type      = string
  default   = ""
  sensitive = true
}

# ClickHouse
variable "clickhouse_cluster_size" {
  type = number
}

variable "clickhouse_username" {
  type    = string
  default = "e2b"
}

variable "clickhouse_password" {
  type      = string
  sensitive = true
}

variable "clickhouse_server_secret" {
  type      = string
  sensitive = true
}

variable "clickhouse_port" {
  type    = number
  default = 9000
}

variable "clickhouse_cpu_count" {
  type    = number
  default = 4
}

variable "clickhouse_memory_mb" {
  type    = number
  default = 8192
}

variable "clickhouse_database" {
  type    = string
  default = "default"
}

variable "clickhouse_metrics_port" {
  type    = number
  default = 9363
}

variable "clickhouse_backups_bucket_name" {
  type = string
}

variable "clickhouse_migrator_repository_name" {
  type = string
}

# Grafana / Observability
variable "grafana_otel_collector_token" {
  type      = string
  sensitive = true
}

variable "grafana_otlp_url" {
  type      = string
  sensitive = true
}

variable "grafana_username" {
  type      = string
  sensitive = true
}

# API
variable "api_port" {
  type    = number
  default = 80
}

variable "api_internal_grpc_port" {
  type    = number
  default = 5009
}

variable "api_memory_mb" {
  type    = number
  default = 512
}

variable "api_cpu_count" {
  type    = number
  default = 1
}

variable "api_repository_name" {
  type = string
}

variable "db_migrator_repository_name" {
  type = string
}

variable "postgres_connection_string" {
  type      = string
  sensitive = true
}

variable "auth_provider_config" {
  type = object({
    jwt = optional(list(object({
      issuer = object({
        url                 = string
        discoveryURL        = optional(string)
        audiences           = list(string)
        audienceMatchPolicy = optional(string)
      })
      claimMappings = optional(object({
        username = object({
          claim = string
        })
      }))
      jwksCacheDuration = optional(string)
    })))
    bearer = optional(list(object({
      hmac = object({
        secrets = list(string)
      })
      claimMappings = optional(object({
        username = object({
          claim = string
        })
      }))
    })))
  })
  sensitive = true
  default   = null
}

variable "admin_token" {
  type      = string
  sensitive = true
}

variable "sandbox_access_token_hash_seed" {
  type      = string
  sensitive = true
}

# Orchestrator
variable "orchestrator_node_pool" {
  type = string
}

variable "orchestrator_port" {
  type    = number
  default = 5008
}

variable "orchestrator_proxy_port" {
  type    = number
  default = 5007
}

variable "allow_sandbox_internet" {
  type    = bool
  default = true
}

variable "allow_sandbox_internal_cidrs" {
  type        = string
  description = "Comma-separated CIDRs to allow through the sandbox firewall deny list"
  default     = ""
}

variable "envd_timeout" {
  type    = string
  default = "40s"
}

variable "fc_env_pipeline_bucket_name" {
  type = string
}

variable "template_bucket_name" {
  type = string
}

variable "build_cache_bucket_name" {
  type    = string
  default = ""
}

variable "custom_environments_repository_name" {
  type = string
}

# Template Manager
variable "build_node_pool" {
  type = string
}

variable "template_manager_port" {
  type    = number
  default = 5008
}

variable "api_secret" {
  type      = string
  sensitive = true
}

variable "build_cluster_size" {
  type    = number
  default = 1
}

# Loki
variable "loki_bucket_name" {
  type = string
}

variable "loki_port" {
  type    = number
  default = 3100
}

variable "logs_health_proxy_port" {
  type    = number
  default = 44313
}

# Telemetry
variable "otel_collector_grpc_port" {
  type    = number
  default = 4317
}

variable "logs_proxy_port" {
  type    = number
  default = 30006
}

# Feature flags
variable "launch_darkly_api_key" {
  type      = string
  default   = ""
  sensitive = true
}

variable "traefik_config_files" {
  type = map(string)
}

variable "db_max_open_connections" {
  type = number
}

variable "db_min_idle_connections" {
  type = number
}

variable "auth_db_max_open_connections" {
  type = number
}

variable "auth_db_min_idle_connections" {
  type = number
}
