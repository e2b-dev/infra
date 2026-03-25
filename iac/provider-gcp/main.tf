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

    google-beta = {
      source  = "hashicorp/google-beta"
      version = "6.50.0"
    }

    nomad = {
      source  = "hashicorp/nomad"
      version = "2.1.0"
    }

    random = {
      source  = "hashicorp/random"
      version = "3.8.1"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}

provider "google-beta" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}

data "google_secret_manager_secret_version" "routing_domains" {
  secret = module.init.routing_domains_secret_name
}

locals {
  additional_domains = nonsensitive(jsondecode(data.google_secret_manager_secret_version.routing_domains.secret_data))
  additional_api_services = (
    length(var.additional_api_services) > 0 ? var.additional_api_services :
    var.additional_api_services_json != "" ? jsondecode(var.additional_api_services_json) :
    []
  )


  // Check if all clusters has size greater than 1
  template_manages_clusters_size_gt_1 = alltrue([for c in var.build_clusters_config : (c.cluster_size > 1)])

  // for more docs, see https://linux.die.net/man/5/nfs
  default_persistent_volume_type_nfs_mount_options = [
    // network
    "hard",       // retry nfs requests indefinitely until they succeed, never fail
    "async",      // write eventually
    "nconnect=7", // use multiple connections
    "noresvport", // use a non-privileged source port
    "retrans=3",  // retry two times before performing recovery actions
    "timeo=600",  // wait 60 seconds (measured in deci-seconds) before retrying a failed request

    // resiliency
    "fg",              // wait for mounts to finish before exiting
    "cto",             // enable "close-to-open" attribute checks
    "lock",            // enable network locking
    "local_lock=none", // all locks are network locks

    // caching
    "noac",             // disable attribute caching. slower, but more reliable
    "lookupcache=none", // disable lookup caching

    // security
    "noacl",   // do not use an acl
    "sec=sys", // use AUTH_SYS for all requests
  ]

  persistent_volume_types = {
    for key, config in var.persistent_volume_types : key => {
      local_mount_path = "/mnt/persistent-volume-types/${key}"
      nfs_location     = module.persistent-volume-types[key].nfs_location
      nfs_mount_opts = join(",", concat(
        [format("nfsvers=%s", module.persistent-volume-types[key].nfs_version)],
        config.mount_options != null ? config.mount_options : local.default_persistent_volume_type_nfs_mount_options,
      ))
    }
  }
}

module "init" {
  source = "./init"

  labels        = var.labels
  prefix        = var.prefix
  bucket_prefix = var.bucket_prefix

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  template_bucket_location = var.template_bucket_location
  template_bucket_name     = var.template_bucket_name
}

module "cluster" {
  source = "./nomad-cluster"

  environment = var.environment

  gcp_project_id                   = var.gcp_project_id
  gcp_region                       = var.gcp_region
  gcp_zone                         = var.gcp_zone
  google_service_account_key       = module.init.google_service_account_key
  network_name                     = var.network_name

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

  additional_domains                      = local.additional_domains
  additional_api_services                 = local.additional_api_services
  additional_api_paths_handled_by_ingress = var.additional_api_paths_handled_by_ingress

  docker_contexts_bucket_name = module.init.envs_docker_context_bucket_name
  cluster_setup_bucket_name   = module.init.cluster_setup_bucket_name
  fc_env_pipeline_bucket_name = module.init.fc_env_pipeline_bucket_name
  fc_kernels_bucket_name      = module.init.fc_kernels_bucket_name
  fc_versions_bucket_name     = module.init.fc_versions_bucket_name

  allowed_source_ip = var.allowed_source_ip

  clickhouse_job_constraint_prefix = var.clickhouse_job_constraint_prefix
  clickhouse_health_port           = var.clickhouse_health_port

  consul_acl_token_secret = module.init.consul_acl_token_secret
  nomad_acl_token_secret  = module.init.nomad_acl_token_secret

  filestore_cache_enabled     = var.filestore_cache_enabled
  filestore_cache_tier        = var.filestore_cache_tier
  filestore_cache_capacity_gb = var.filestore_cache_capacity_gb
  filestore_nfs_version       = var.filestore_nfs_version

  persistent_volume_types = local.persistent_volume_types

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

  consul_acl_token_secret = module.init.consul_acl_token_secret
  nomad_acl_token_secret  = module.init.nomad_acl_token_secret
  nomad_port              = var.nomad_port
  core_repository_name    = module.init.core_repository_name

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
  ingress_port                 = var.ingress_port
  ingress_count                = var.ingress_count
  additional_traefik_arguments = var.additional_traefik_arguments

  # API
  api_server_count                                       = var.api_server_count
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
  sandbox_storage_backend                                = var.sandbox_storage_backend
  db_max_open_connections                                = var.db_max_open_connections
  db_min_idle_connections                                = var.db_min_idle_connections
  auth_db_max_open_connections                           = var.auth_db_max_open_connections
  auth_db_min_idle_connections                           = var.auth_db_min_idle_connections

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

  # Dashboard API
  dashboard_api_count = var.dashboard_api_count

  # Docker reverse proxy
  docker_reverse_proxy_port                = var.docker_reverse_proxy_port
  docker_reverse_proxy_service_account_key = google_service_account_key.google_service_key.private_key

  # Orchestrator
  orchestrator_node_pool         = var.orchestrator_node_pool
  allow_sandbox_internet         = var.allow_sandbox_internet
  orchestrator_port              = var.orchestrator_port
  orchestrator_proxy_port        = var.orchestrator_proxy_port
  fc_env_pipeline_bucket_name    = module.init.fc_env_pipeline_bucket_name
  envd_timeout                   = var.envd_timeout
  persistent_volume_mounts       = { for key, config in local.persistent_volume_types : key => config["local_mount_path"] }
  default_persistent_volume_type = var.default_persistent_volume_type
  orchestrator_env_vars          = var.orchestrator_env_vars

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

  volume_token_issuer           = local.volume_token_issuer
  volume_token_signing_key      = local.volume_token_signing_key
  volume_token_signing_key_name = local.volume_token_signature_name
  volume_token_signing_method   = local.volume_token_signature_method
  volume_token_duration         = var.volume_token_valid_for

  gcs_grpc_connection_pool_size = var.gcs_grpc_connection_pool_size
}


module "redis" {
  source = "./redis"
  count  = var.redis_managed ? 1 : 0

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region
  gcp_zone       = var.gcp_zone
  network_name   = var.network_name

  redis_cluster_url_secret_version   = module.init.redis_cluster_url_secret_version
  redis_tls_ca_base64_secret_version = module.init.redis_tls_ca_base64_secret_version

  shard_count = var.redis_shard_count
  prefix      = var.prefix
}

module "remote_repository" {
  source = "./remote-repository"

  count = var.remote_repository_enabled ? 1 : 0

  depends_on = [module.init]

  prefix = var.prefix

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  google_service_account_email = module.init.service_account_email

  dockerhub_username_secret_name = module.init.dockerhub_username_secret_name
  dockerhub_password_secret_name = module.init.dockerhub_password_secret_name
}
