terraform {
  required_version = ">= 1.5.0, < 1.6.0"

  backend "gcs" {
    prefix = "terraform/orchestration/state"
  }

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "6.50.0"
    }

    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.19.0"
    }

    nomad = {
      source  = "hashicorp/nomad"
      version = "2.1.0"
    }

    random = {
      source  = "hashicorp/random"
      version = "3.5.1"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}

data "google_secret_manager_secret_version" "routing_domains" {
  secret = module.init.routing_domains_secret_name
}

locals {
  additional_domains = nonsensitive(jsondecode(data.google_secret_manager_secret_version.routing_domains.secret_data))

  // Check if all clusters has size greater than 1
  template_manages_clusters_size_gt_1 = alltrue([for c in var.build_clusters_config : c.cluster_size > 1])
}

module "init" {
  source = "./init"

  labels = var.labels
  prefix = var.prefix

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  template_bucket_location = var.template_bucket_location
  template_bucket_name     = var.template_bucket_name
}

module "cluster" {
  source = "./nomad-cluster"

  environment = var.environment

  cloudflare_api_token_secret_name = module.init.cloudflare_api_token_secret_name
  gcp_project_id                   = var.gcp_project_id
  gcp_region                       = var.gcp_region
  gcp_zone                         = var.gcp_zone
  google_service_account_key       = module.init.google_service_account_key

  build_clusters_config  = var.build_clusters_config
  client_clusters_config = var.client_clusters_config

  api_cluster_size        = var.api_cluster_size
  clickhouse_cluster_size = var.clickhouse_cluster_size
  server_cluster_size     = var.server_cluster_size
  loki_cluster_size       = var.loki_cluster_size

  server_machine_type     = var.server_machine_type
  api_machine_type        = var.api_machine_type
  clickhouse_machine_type = var.clickhouse_machine_type
  loki_machine_type       = var.loki_machine_type

  api_node_pool          = var.api_node_pool
  build_node_pool        = var.build_node_pool
  clickhouse_node_pool   = var.clickhouse_node_pool
  loki_node_pool         = var.loki_node_pool
  orchestrator_node_pool = var.orchestrator_node_pool

  api_use_nat              = var.api_use_nat
  api_nat_ips              = var.api_nat_ips
  api_nat_min_ports_per_vm = var.api_nat_min_ports_per_vm

  client_proxy_port        = var.client_proxy_port
  client_proxy_health_port = var.client_proxy_health_port

  ingress_port                 = var.ingress_port
  api_port                     = var.api_port
  docker_reverse_proxy_port    = var.docker_reverse_proxy_port
  nomad_port                   = var.nomad_port
  google_service_account_email = module.init.service_account_email
  domain_name                  = var.domain_name

  additional_domains = local.additional_domains
  additional_api_services = (var.additional_api_services_json != "" ?
    jsondecode(var.additional_api_services_json) :
  [])

  docker_contexts_bucket_name = module.init.envs_docker_context_bucket_name
  cluster_setup_bucket_name   = module.init.cluster_setup_bucket_name
  fc_env_pipeline_bucket_name = module.init.fc_env_pipeline_bucket_name
  fc_kernels_bucket_name      = module.init.fc_kernels_bucket_name
  fc_versions_bucket_name     = module.init.fc_versions_bucket_name

  clickhouse_job_constraint_prefix = var.clickhouse_job_constraint_prefix
  clickhouse_health_port           = var.clickhouse_health_port

  consul_acl_token_secret = module.init.consul_acl_token_secret
  nomad_acl_token_secret  = module.init.nomad_acl_token_secret

  filestore_cache_enabled     = var.filestore_cache_enabled
  filestore_cache_tier        = var.filestore_cache_tier
  filestore_cache_capacity_gb = var.filestore_cache_capacity_gb

  labels = var.labels
  prefix = var.prefix

  # Boot disks
  api_boot_disk_type        = var.api_boot_disk_type
  server_boot_disk_type     = var.server_boot_disk_type
  server_boot_disk_size_gb  = var.server_boot_disk_size_gb
  clickhouse_boot_disk_type = var.clickhouse_boot_disk_type
  loki_boot_disk_type       = var.loki_boot_disk_type
}

module "nomad" {
  source = "./nomad"

  prefix         = var.prefix
  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region
  gcp_zone       = var.gcp_zone

  consul_acl_token_secret       = module.init.consul_acl_token_secret
  nomad_acl_token_secret        = module.init.nomad_acl_token_secret
  nomad_port                    = var.nomad_port
  otel_tracing_print            = var.otel_tracing_print
  orchestration_repository_name = module.init.orchestration_repository_name

  # Clickhouse
  clickhouse_resources_cpu_count   = var.clickhouse_resources_cpu_count
  clickhouse_resources_memory_mb   = var.clickhouse_resources_memory_mb
  clickhouse_database              = var.clickhouse_database_name
  clickhouse_backups_bucket_name   = module.init.clickhouse_backups_bucket_name
  clickhouse_server_count          = var.clickhouse_cluster_size
  clickhouse_server_port           = var.clickhouse_server_service_port
  clickhouse_job_constraint_prefix = var.clickhouse_job_constraint_prefix
  clickhouse_node_pool             = var.clickhouse_node_pool

  # Ingress
  ingress_port  = var.ingress_port
  ingress_count = var.ingress_count

  # API
  api_resources_cpu_count                                = var.api_resources_cpu_count
  api_resources_memory_mb                                = var.api_resources_memory_mb
  api_machine_count                                      = var.api_cluster_size
  api_node_pool                                          = var.api_node_pool
  api_port                                               = var.api_port
  environment                                            = var.environment
  google_service_account_key                             = module.init.google_service_account_key
  api_secret                                             = random_password.api_secret.result
  custom_envs_repository_name                            = google_artifact_registry_repository.custom_environments_repository.name
  postgres_connection_string_secret_name                 = module.init.postgres_connection_string_secret_name
  postgres_read_replica_connection_string_secret_version = google_secret_manager_secret_version.postgres_read_replica_connection_string
  supabase_jwt_secrets_secret_name                       = module.init.supabase_jwt_secret_name
  posthog_api_key_secret_name                            = module.init.posthog_api_key_secret_name
  analytics_collector_host_secret_name                   = module.init.analytics_collector_host_secret_name
  analytics_collector_api_token_secret_name              = module.init.analytics_collector_api_token_secret_name
  api_admin_token                                        = random_password.api_admin_secret.result
  redis_cluster_url_secret_version                       = module.init.redis_cluster_url_secret_version
  redis_tls_ca_base64_secret_version                     = module.init.redis_tls_ca_base64_secret_version
  sandbox_access_token_hash_seed                         = random_password.sandbox_access_token_hash_seed.result

  # Click Proxy
  client_proxy_count               = var.client_proxy_count
  client_proxy_resources_cpu_count = var.client_proxy_resources_cpu_count
  client_proxy_resources_memory_mb = var.client_proxy_resources_memory_mb
  client_proxy_update_max_parallel = var.client_proxy_update_max_parallel

  client_proxy_session_port = var.client_proxy_port.port
  client_proxy_health_port  = var.client_proxy_health_port.port

  domain_name = var.domain_name

  # Logs
  loki_node_pool           = var.loki_node_pool
  loki_machine_count       = var.loki_cluster_size
  loki_resources_memory_mb = var.loki_resources_memory_mb
  loki_resources_cpu_count = var.loki_resources_cpu_count
  loki_use_v13_schema_from = var.loki_use_v13_schema_from
  loki_bucket_name         = module.init.loki_bucket_name
  loki_service_port        = var.loki_service_port

  # Otel Colelctor
  otel_collector_resources_memory_mb = var.otel_collector_resources_memory_mb
  otel_collector_resources_cpu_count = var.otel_collector_resources_cpu_count

  # Docker reverse proxy
  docker_reverse_proxy_port                = var.docker_reverse_proxy_port
  docker_reverse_proxy_service_account_key = google_service_account_key.google_service_key.private_key

  # Orchestrator
  orchestrator_node_pool      = var.orchestrator_node_pool
  allow_sandbox_internet      = var.allow_sandbox_internet
  orchestrator_port           = var.orchestrator_port
  orchestrator_proxy_port     = var.orchestrator_proxy_port
  fc_env_pipeline_bucket_name = module.init.fc_env_pipeline_bucket_name
  envd_timeout                = var.envd_timeout

  # Template manager
  builder_node_pool                   = var.build_node_pool
  template_manager_port               = var.template_manager_port
  template_bucket_name                = module.init.fc_template_bucket_name
  build_cache_bucket_name             = module.init.fc_build_cache_bucket_name
  template_manages_clusters_size_gt_1 = local.template_manages_clusters_size_gt_1
  dockerhub_remote_repository_url     = var.remote_repository_enabled ? module.remote_repository[0].dockerhub_remote_repository_url : ""

  # Redis
  redis_managed = var.redis_managed
  redis_port    = var.redis_port

  launch_darkly_api_key_secret_name = module.init.launch_darkly_api_key_secret_version.secret

  # Filestore
  shared_chunk_cache_path                       = module.cluster.shared_chunk_cache_path
  filestore_cache_cleanup_disk_usage_target     = var.filestore_cache_cleanup_disk_usage_target
  filestore_cache_cleanup_dry_run               = var.filestore_cache_cleanup_dry_run
  filestore_cache_cleanup_deletions_per_loop    = var.filestore_cache_cleanup_deletions_per_loop
  filestore_cache_cleanup_files_per_loop        = var.filestore_cache_cleanup_files_per_loop
  filestore_cache_cleanup_max_concurrent_stat   = var.filestore_cache_cleanup_max_concurrent_stat
  filestore_cache_cleanup_max_concurrent_scan   = var.filestore_cache_cleanup_max_concurrent_scan
  filestore_cache_cleanup_max_concurrent_delete = var.filestore_cache_cleanup_max_concurrent_delete
  filestore_cache_cleanup_max_retries           = var.filestore_cache_cleanup_max_retries
}

module "redis" {
  source = "./redis"
  count  = var.redis_managed ? 1 : 0

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region
  gcp_zone       = var.gcp_zone

  redis_cluster_url_secret_version   = module.init.redis_cluster_url_secret_version
  redis_tls_ca_base64_secret_version = module.init.redis_tls_ca_base64_secret_version

  prefix = var.prefix
}

module "remote_repository" {
  source = "./remote-repository"

  count = var.remote_repository_enabled ? 1 : 0

  prefix = var.prefix

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  google_service_account_email = module.init.service_account_email
}
