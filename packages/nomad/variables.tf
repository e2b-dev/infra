variable "prefix" {
  type = string
}

variable "gcp_zone" {
  type = string
}

variable "consul_acl_token_secret" {
  type = string
}

variable "template_bucket_name" {
  type = string
}

variable "nomad_acl_token_secret" {
  type = string
}

variable "nomad_port" {
  type = number
}

variable "otel_collector_resources_memory_mb" {
  type = number
}

variable "otel_collector_resources_cpu_count" {
  type = number
}

variable "otel_tracing_print" {
  type = bool
}

# API
variable "api_docker_image_digest" {
  type = string
}

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "api_secret" {
  type = string
}

variable "api_admin_token" {
  type = string
}

variable "sandbox_access_token_hash_seed" {
  type = string
}

variable "logs_proxy_address" {
  type = string
}

variable "environment" {
  type = string
}

variable "api_machine_count" {
  type = number
}

variable "api_dns_port_number" {
  type    = number
  default = 5353
}

variable "custom_envs_repository_name" {
  type = string
}

variable "gcp_project_id" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "google_service_account_key" {
  type = string
}

variable "posthog_api_key_secret_name" {
  type = string
}

variable "postgres_connection_string_secret_name" {
  type = string
}

variable "supabase_jwt_secrets_secret_name" {
  type = string
}

variable "client_proxy_count" {
  type = number
}

variable "client_proxy_resources_memory_mb" {
  type = number
}

variable "client_proxy_resources_cpu_count" {
  type = number
}

variable "client_proxy_docker_image_digest" {
  type = string
}

variable "client_proxy_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
}

variable "client_proxy_port" {
  type = object({
    name = string
    port = number
  })
}

variable "domain_name" {
  type = string
}

# Telemetry
variable "logs_proxy_port" {
  type = object({
    name = string
    port = number
  })
}

variable "logs_health_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "analytics_collector_host_secret_name" {
  type = string
}

variable "analytics_collector_api_token_secret_name" {
  type = string
}

variable "launch_darkly_api_key_secret_name" {
  type = string
}

variable "clickhouse_bucket_name" {
  type = string
}

variable "loki_resources_memory_mb" {
  type = number
}

variable "loki_resources_cpu_count" {
  type = number
}

variable "loki_bucket_name" {
  type = string
}

variable "loki_service_port" {
  type = object({
    name = string
    port = number
  })
}

variable "redis_url_secret_version" {
  type = any
}

# Docker reverse proxy
variable "docker_reverse_proxy_docker_image_digest" {
  type = string
}

variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "docker_reverse_proxy_service_account_key" {
  type = string
}

# Orchestrator
variable "orchestrator_port" {
  type = number
}

variable "orchestrator_proxy_port" {
  type = number
}

variable "fc_env_pipeline_bucket_name" {
  type = string
}

variable "client_machine_type" {
  type = string
}

variable "allow_sandbox_internet" {
  type = bool
}

# Template manager
variable "template_manager_port" {
  type = number
}

variable "template_manager_machine_count" {
  type = number
}

# Redis
variable "redis_port" {
  type = object({
    name = string
    port = number
  })
}

# Clickhouse
variable "clickhouse_username" {
  type    = string
  default = "e2b"
}

variable "clickhouse_database" {
  type = string
}

variable "clickhouse_server_count" {
  type = number
}

variable "clickhouse_metrics_port" {
  type    = number
  default = 9363
}

variable "otel_collector_grpc_port" {
  type = number
  default = 4317
}
variable "clickhouse_server_port" {
  type = object({
    name = string
    port = number
  })
}

variable "clickhouse_job_constraint_prefix" {
  description = "The prefix to use for the job constraint of the instance in the metadata."
  type        = string
}

variable "clickhouse_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
}

variable "write_clickhouse_metrics" {
  description = "Whether to write metrics to ClickHouse"
  type        = bool
}
