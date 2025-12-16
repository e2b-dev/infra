variable "datacenter" { type = string }
variable "nomad_address" { type = string }
variable "consul_address" { type = string }

variable "enable_nomad_jobs" {
  type    = bool
  default = true
}
variable "ingress_node_ip" { type = string }
variable "nomad_acl_token" {
  type    = string
  default = ""
}
variable "consul_acl_token" {
  type    = string
  default = ""
}

variable "api_node_pool" { type = string }
variable "ingress_count" { type = number }
variable "api_machine_count" { type = number }
variable "api_resources_cpu_count" { type = number }
variable "api_resources_memory_mb" { type = number }

variable "api_port" { type = object({ name = string, port = number, health_path = string }) }
variable "ingress_port" { type = object({ name = string, port = number, health_path = string }) }
variable "edge_api_port" { type = object({ name = string, port = number, path = string }) }
variable "edge_proxy_port" { type = object({ name = string, port = number }) }
variable "logs_proxy_port" { type = object({ name = string, port = number }) }
variable "loki_service_port" {
  type    = object({ name = string, port = number })
  default = { name = "loki", port = 31000 }
}
variable "grafana_service_port" {
  type    = object({ name = string, port = number })
  default = { name = "grafana", port = 30008 }
}
variable "grafana_resources_memory_mb" {
  type    = number
  default = 512
}
variable "grafana_resources_cpu_count" {
  type    = number
  default = 0.5
}

variable "domain_name" { type = string }

variable "api_admin_token" { type = string }
variable "environment" { type = string }
variable "edge_api_secret" { type = string }

variable "postgres_connection_string" { type = string }
variable "supabase_jwt_secrets" { type = string }
variable "posthog_api_key" { type = string }
variable "analytics_collector_host" { type = string }
variable "analytics_collector_api_token" { type = string }
variable "redis_url" { type = string }
variable "launch_darkly_api_key" {
  type    = string
  default = ""
}

variable "orchestrator_port" { type = number }
variable "orchestrator_proxy_port" { type = number }
variable "template_manager_port" { type = number }
variable "sandbox_hyperloop_proxy_port" {
  type    = number
  default = 5011
}
variable "use_local_namespace_storage" {
  type    = bool
  default = false
}
variable "otel_collector_grpc_port" {
  type    = number
  default = 4317
}

variable "otel_tracing_print" {
  type    = bool
  default = false
}

variable "api_image" { type = string }
variable "db_migrator_image" { type = string }
variable "client_proxy_image" { type = string }
variable "docker_reverse_proxy_image" { type = string }

variable "orchestrator_artifact_url" { type = string }
variable "template_manager_artifact_url" { type = string }
variable "envd_artifact_url" { type = string }
variable "fc_artifact_node_pools" { type = list(string) }
variable "orchestrator_node_pool" { type = string }
variable "builder_node_pool" { type = string }
variable "template_bucket_name" { type = string }
variable "build_cache_bucket_name" { type = string }
variable "envd_timeout" { type = string }
variable "allow_sandbox_internet" { type = bool }
variable "shared_chunk_cache_path" { type = string }
variable "dockerhub_remote_repository_url" { type = string }
variable "dockerhub_remote_repository_provider" {
  type    = string
  default = ""
}
variable "docker_image_prefix" {
  type    = string
  default = ""
}
variable "api_secret" { type = string }
variable "redis_tls_ca_base64" { type = string }
variable "redis_secure_cluster_url" { type = string }

variable "otel_collector_resources_memory_mb" { type = number }
variable "otel_collector_resources_cpu_count" { type = number }
variable "loki_resources_memory_mb" { type = number }
variable "loki_resources_cpu_count" { type = number }
variable "template_manager_machine_count" { type = number }
variable "logs_health_proxy_port" { type = object({ name = string, port = number, health_path = string }) }

variable "clickhouse_username" {
  type    = string
  default = "e2b"
}
variable "clickhouse_database" { type = string }
variable "clickhouse_server_count" {
  type    = number
  default = 0
}
variable "clickhouse_server_port" { type = object({ name = string, port = number }) }
variable "clickhouse_resources_memory_mb" { type = number }
variable "clickhouse_resources_cpu_count" { type = number }
variable "clickhouse_metrics_port" { type = number }
variable "clickhouse_version" { type = string }
variable "sandbox_access_token_hash_seed" {
  type    = string
  default = ""
}

variable "use_nfs_share_storage" {
  type    = bool
  default = false
}

variable "nfs_server_ip" {
  type    = string
  default = ""
}

variable "enable_network_policy_job" {
  type    = bool
  default = false
}

variable "network_open_ports" {
  type    = list(string)
  default = ["2049/tcp", "111/tcp", "111/udp"]
}
