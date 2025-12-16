variable "api_node_pool" { type = string }
variable "ingress_count" { type = number }
variable "api_machine_count" { type = number }
variable "api_resources_cpu_count" { type = number }
variable "api_resources_memory_mb" { type = number }

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "ingress_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "edge_api_port" {
  type = object({
    name = string
    port = number
    path = string
  })
}

variable "edge_proxy_port" {
  type = object({
    name = string
    port = number
  })
}

variable "logs_proxy_port" {
  type = object({
    name = string
    port = number
  })
}

variable "loki_service_port" {
  type = object({
    name = string
    port = number
  })
}

variable "grafana_service_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "grafana"
    port = 30008
  }
}
variable "grafana_resources_memory_mb" {
  type    = number
  default = 512
}
variable "grafana_resources_cpu_count" {
  type    = number
  default = 1
}

variable "api_admin_token" { type = string }
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
variable "template_manager_port" { type = number }
variable "otel_collector_grpc_port" {
  type    = number
  default = 4317
}

variable "api_image" {
  type    = string
  default = ""
}
variable "db_migrator_image" {
  type    = string
  default = ""
}
variable "client_proxy_image" {
  type    = string
  default = ""
}
variable "docker_reverse_proxy_image" {
  type    = string
  default = ""
}

variable "docker_image_prefix" {
  type    = string
  default = ""
}

variable "docker_http_proxy" {
  type    = string
  default = ""
}

variable "docker_https_proxy" {
  type    = string
  default = ""
}

variable "docker_no_proxy" {
  type    = string
  default = ""
}

variable "clickhouse_database" {
  type    = string
  default = ""
}
variable "clickhouse_server_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "clickhouse"
    port = 9000
  }
}
variable "sandbox_access_token_hash_seed" {
  type    = string
  default = ""
}

variable "orchestrator_proxy_port" { type = number }
variable "orchestrator_artifact_url" { type = string }
variable "template_manager_artifact_url" { type = string }
variable "envd_artifact_url" {
  type    = string
  default = ""
}
variable "orchestrator_node_pool" { type = string }
variable "builder_node_pool" { type = string }
variable "template_bucket_name" {
  type    = string
  default = ""
}
variable "build_cache_bucket_name" {
  type    = string
  default = ""
}
variable "envd_timeout" { type = string }
variable "allow_sandbox_internet" { type = bool }
variable "shared_chunk_cache_path" {
  type    = string
  default = ""
}
variable "dockerhub_remote_repository_url" {
  type    = string
  default = ""
}
variable "dockerhub_remote_repository_provider" {
  type    = string
  default = ""
}
variable "api_secret" { type = string }
variable "redis_tls_ca_base64" {
  type    = string
  default = ""
}
variable "redis_secure_cluster_url" {
  type    = string
  default = ""
}
variable "use_local_namespace_storage" {
  type    = bool
  default = false
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
variable "otel_collector_resources_memory_mb" { type = number }
variable "otel_collector_resources_cpu_count" { type = number }
variable "loki_resources_memory_mb" { type = number }
variable "loki_resources_cpu_count" { type = number }
variable "template_manager_machine_count" {
  type    = number
  default = 1
}
variable "logs_health_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "clickhouse_resources_memory_mb" { type = number }
variable "clickhouse_resources_cpu_count" { type = number }
variable "clickhouse_metrics_port" { type = number }

variable "artifact_scp_host" {
  type    = string
  default = ""
}

variable "artifact_scp_user" {
  type    = string
  default = ""
}

variable "artifact_scp_ssh_key" {
  type    = string
  default = ""
}

variable "artifact_scp_dir" {
  type    = string
  default = "/var/www/artifacts"
}

variable "artifact_scp_port" {
  type    = number
  default = 0
}

variable "enable_artifact_scp_server" {
  type    = bool
  default = true
}

variable "default_kernel_version" {
  type    = string
  default = "6.1.0"
}

variable "default_firecracker_version" {
  type    = string
  default = "1.4.0"
}

variable "kernel_source_base_url" {
  type    = string
  default = "https://storage.googleapis.com/e2b-prod-public-builds/kernels"
}

variable "firecracker_source_base_url" {
  type    = string
  default = "https://storage.googleapis.com/e2b-prod-public-builds/firecrackers"
}

variable "fc_artifact_node_pools" {
  type    = list(string)
  default = []
}

variable "enable_nomad_jobs" {
  type    = bool
  default = true
}

variable "enable_nodes_docker_proxy" {
  type    = bool
  default = true
}

variable "enable_nodes_fc_artifacts" {
  type    = bool
  default = true
}

variable "enable_nodes_uninstall" {
  type    = bool
  default = false
}

variable "uninstall_version" {
  type    = string
  default = "v1"
}

variable "uninstall_confirm_phrase" {
  type    = string
  default = ""
}
