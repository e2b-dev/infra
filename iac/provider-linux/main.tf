terraform {
  required_version = ">= 1.3.0"
}

variable "environment" { type = string }
variable "datacenter" { type = string }
variable "domain_name" { type = string }

variable "servers_json" { type = string }
variable "clients_json" { type = string }

variable "nomad_address" { type = string }
variable "nomad_acl_token" {
  type    = string
  default = ""
}
variable "consul_acl_token" {
  type    = string
  default = ""
}

locals {
  servers = jsondecode(var.servers_json)
  clients = jsondecode(var.clients_json)
}

module "machines" {
  source                      = "./machines"
  datacenter                  = var.datacenter
  servers                     = local.servers
  clients                     = local.clients
  consul_acl_token            = var.consul_acl_token
  docker_image_prefix         = var.docker_image_prefix
  nomad_acl_token             = var.nomad_acl_token
  docker_http_proxy           = var.docker_http_proxy
  docker_https_proxy          = var.docker_https_proxy
  docker_no_proxy             = var.docker_no_proxy
  builder_node_pool           = var.builder_node_pool
  orchestrator_node_pool      = var.orchestrator_node_pool
  kernel_source_base_url      = var.kernel_source_base_url
  firecracker_source_base_url = var.firecracker_source_base_url
  default_kernel_version      = var.default_kernel_version
  default_firecracker_version = var.default_firecracker_version
  enable_nodes_docker_proxy   = var.enable_nodes_docker_proxy
  enable_nodes_fc_artifacts   = var.enable_nodes_fc_artifacts
  enable_nodes_uninstall      = var.enable_nodes_uninstall
  uninstall_version           = var.uninstall_version
  uninstall_confirm_phrase    = var.uninstall_confirm_phrase
}

module "nomad" {
  source           = "./nomad"
  datacenter       = var.datacenter
  nomad_address    = var.nomad_address
  nomad_acl_token  = var.nomad_acl_token
  consul_acl_token = var.consul_acl_token
  consul_address   = replace(var.nomad_address, ":4646", ":8500")
  ingress_node_ip  = replace(replace(var.nomad_address, "http://", ""), ":4646", "")

  api_node_pool           = var.api_node_pool
  ingress_count           = var.ingress_count
  api_machine_count       = var.api_machine_count
  api_resources_cpu_count = var.api_resources_cpu_count
  api_resources_memory_mb = var.api_resources_memory_mb

  api_port             = var.api_port
  ingress_port         = var.ingress_port
  edge_api_port        = var.edge_api_port
  edge_proxy_port      = var.edge_proxy_port
  logs_proxy_port      = var.logs_proxy_port
  loki_service_port    = var.loki_service_port
  grafana_service_port = var.grafana_service_port

  api_admin_token = var.api_admin_token
  environment     = var.environment
  edge_api_secret = var.edge_api_secret

  postgres_connection_string    = var.postgres_connection_string
  supabase_jwt_secrets          = var.supabase_jwt_secrets
  posthog_api_key               = var.posthog_api_key
  analytics_collector_host      = var.analytics_collector_host
  analytics_collector_api_token = var.analytics_collector_api_token
  redis_url                     = var.redis_url
  launch_darkly_api_key         = var.launch_darkly_api_key

  orchestrator_port        = var.orchestrator_port
  template_manager_port    = var.template_manager_port
  otel_collector_grpc_port = var.otel_collector_grpc_port

  api_image                      = var.api_image
  db_migrator_image              = var.db_migrator_image
  client_proxy_image             = var.client_proxy_image
  docker_reverse_proxy_image     = var.docker_reverse_proxy_image
  sandbox_access_token_hash_seed = var.sandbox_access_token_hash_seed

  clickhouse_username                  = "e2b"
  clickhouse_database                  = var.clickhouse_database
  clickhouse_server_count              = 0
  clickhouse_server_port               = var.clickhouse_server_port
  clickhouse_resources_memory_mb       = var.clickhouse_resources_memory_mb
  clickhouse_resources_cpu_count       = var.clickhouse_resources_cpu_count
  clickhouse_metrics_port              = var.clickhouse_metrics_port
  clickhouse_version                   = "24.3.3"
  api_secret                           = var.api_secret
  otel_collector_resources_memory_mb   = var.otel_collector_resources_memory_mb
  otel_collector_resources_cpu_count   = var.otel_collector_resources_cpu_count
  orchestrator_artifact_url            = var.orchestrator_artifact_url
  template_manager_artifact_url        = var.template_manager_artifact_url
  envd_artifact_url                    = var.envd_artifact_url
  fc_artifact_node_pools               = var.fc_artifact_node_pools
  template_manager_machine_count       = var.template_manager_machine_count
  logs_health_proxy_port               = var.logs_health_proxy_port
  template_bucket_name                 = var.template_bucket_name
  build_cache_bucket_name              = var.build_cache_bucket_name
  loki_resources_memory_mb             = var.loki_resources_memory_mb
  loki_resources_cpu_count             = var.loki_resources_cpu_count
  grafana_resources_memory_mb          = var.grafana_resources_memory_mb
  grafana_resources_cpu_count          = var.grafana_resources_cpu_count
  redis_tls_ca_base64                  = var.redis_tls_ca_base64
  shared_chunk_cache_path              = var.shared_chunk_cache_path
  dockerhub_remote_repository_url      = var.dockerhub_remote_repository_url
  dockerhub_remote_repository_provider = var.dockerhub_remote_repository_provider
  docker_image_prefix                  = var.docker_image_prefix
  enable_nomad_jobs                    = var.enable_nodes_uninstall ? false : var.enable_nomad_jobs
  orchestrator_proxy_port              = var.orchestrator_proxy_port
  orchestrator_node_pool               = var.orchestrator_node_pool
  redis_secure_cluster_url             = var.redis_secure_cluster_url
  allow_sandbox_internet               = var.allow_sandbox_internet
  builder_node_pool                    = var.builder_node_pool
  envd_timeout                         = var.envd_timeout
  domain_name                          = var.domain_name
  use_local_namespace_storage          = var.use_local_namespace_storage

  use_nfs_share_storage = var.use_nfs_share_storage
  nfs_server_ip         = var.nfs_server_ip

  enable_network_policy_job = var.enable_network_policy_job
  network_open_ports        = var.network_open_ports
}

resource "null_resource" "artifact_scp_server" {
  count = (var.enable_nodes_uninstall ? 0 : (var.enable_artifact_scp_server && var.artifact_scp_host != "" ? 1 : 0))

  triggers = {
    url1 = var.orchestrator_artifact_url
    url2 = var.template_manager_artifact_url
    dir  = var.artifact_scp_dir
  }

  connection {
    type        = "ssh"
    host        = var.artifact_scp_host
    user        = var.artifact_scp_user
    private_key = file(var.artifact_scp_ssh_key)
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "sudo apt-get update -y",
      "sudo apt-get install -y nginx",
      "sudo mkdir -p ${var.artifact_scp_dir}",
      "sudo chown -R www-data:www-data ${var.artifact_scp_dir}",
      "URL=\"${var.orchestrator_artifact_url}\"",
      "PORT=$(echo \"$URL\" | sed -E 's|^https?://[^:/]+:([0-9]+).*|\\1|')",
      "[ -z \"$PORT\" ] && PORT=80",
      "printf 'server {\\n    listen %s;\\n    server_name _;\\n    root %s;\\n    location / {\\n        autoindex on;\\n    }\\n}\\n' \"$PORT\" \"${var.artifact_scp_dir}\" | sudo tee /etc/nginx/sites-available/artifacts >/dev/null",
      "sudo ln -sf /etc/nginx/sites-available/artifacts /etc/nginx/sites-enabled/artifacts",
      "sudo rm -f /etc/nginx/sites-enabled/default || true",
      "sudo systemctl restart nginx"
    ]
  }
}
