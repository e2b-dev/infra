terraform {
  required_version = ">=1.5.0"

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

    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.52.5"
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

  clickhouse_username          = "e2b"
  clickhouse_connection_string = var.clickhouse_cluster_size > 0 ? "clickhouse://${local.clickhouse_username}:${random_password.clickhouse_password.result}@clickhouse.service.consul:${var.clickhouse_server_service_port.port}/${var.clickhouse_database_name}" : ""
  redis_url                    = trimspace(data.google_secret_manager_secret_version.redis_cluster_url.secret_data) == "" ? "redis.service.consul:${var.redis_port.port}" : ""
  redis_cluster_url            = trimspace(data.google_secret_manager_secret_version.redis_cluster_url.secret_data)
  loki_url                     = "http://loki.service.consul:${var.loki_service_port.port}"
  logs_proxy_port              = 30006
  otel_collector_grpc_port     = 4317

  default_auth_provider_config = {
    jwt = []
  }
  # jsonencode/jsondecode strips Terraform's static type info from
  # var.auth_provider_config so that the conditional below does not fail with
  # "Inconsistent conditional result types" when the typed object literal in
  # `default_auth_provider_config` (a tuple of objects) is compared with the
  # variable's declared object type (a list of objects).
  auth_provider_config = var.auth_provider_config != null ? jsondecode(jsonencode(var.auth_provider_config)) : local.default_auth_provider_config

  # The Nomad jobspec template renders each entry as `${key} = "${value}"`,
  # so values that themselves contain `"` characters (like a JSON blob)
  # must have those quotes pre-escaped to produce valid HCL.
  api_env_vars = merge({
    ENVIRONMENT                    = var.environment
    GIN_MODE                       = "release"
    DOMAIN_NAME                    = var.domain_name
    NOMAD_TOKEN                    = module.init.nomad_acl_token_secret
    ORCHESTRATOR_PORT              = tostring(var.orchestrator_port)
    API_INTERNAL_GRPC_PORT         = tostring(var.api_internal_grpc_port)
    ADMIN_TOKEN                    = trimspace(data.google_secret_manager_secret_version.api_admin_token.secret_data)
    SANDBOX_ACCESS_TOKEN_HASH_SEED = random_password.sandbox_access_token_hash_seed.result
    AUTH_PROVIDER_CONFIG           = replace(jsonencode(local.auth_provider_config), "\"", "\\\"")

    POSTGRES_CONNECTION_STRING             = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
    DB_MAX_OPEN_CONNECTIONS                = tostring(var.db_max_open_connections)
    DB_MIN_IDLE_CONNECTIONS                = tostring(var.db_min_idle_connections)
    AUTH_DB_CONNECTION_STRING              = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
    AUTH_DB_READ_REPLICA_CONNECTION_STRING = trimspace(data.google_secret_manager_secret_version.postgres_read_replica_connection_string.secret_data)
    AUTH_DB_MAX_OPEN_CONNECTIONS           = tostring(var.auth_db_max_open_connections)
    AUTH_DB_MIN_IDLE_CONNECTIONS           = tostring(var.auth_db_min_idle_connections)

    LOKI_URL                     = local.loki_url
    CLICKHOUSE_CONNECTION_STRING = local.clickhouse_connection_string

    POSTHOG_API_KEY               = trimspace(data.google_secret_manager_secret_version.posthog_api_key.secret_data)
    ANALYTICS_COLLECTOR_HOST      = trimspace(data.google_secret_manager_secret_version.analytics_collector_host.secret_data)
    ANALYTICS_COLLECTOR_API_TOKEN = trimspace(data.google_secret_manager_secret_version.analytics_collector_api_token.secret_data)
    LOGS_COLLECTOR_ADDRESS        = "http://localhost:${local.logs_proxy_port}"
    OTEL_COLLECTOR_GRPC_ENDPOINT  = "localhost:${local.otel_collector_grpc_port}"

    REDIS_POOL_SIZE     = "160"
    REDIS_CLUSTER_URL   = local.redis_cluster_url
    REDIS_TLS_CA_BASE64 = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)
    REDIS_URL           = local.redis_url

    LAUNCH_DARKLY_API_KEY = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
    # This is here just because it is required in some part of our code which is transitively imported
    TEMPLATE_BUCKET_NAME           = "skip"
    DEFAULT_PERSISTENT_VOLUME_TYPE = var.default_persistent_volume_type

    VOLUME_TOKEN_ISSUER           = local.volume_token_issuer
    VOLUME_TOKEN_SIGNING_KEY      = local.volume_token_signing_key
    VOLUME_TOKEN_SIGNING_KEY_NAME = local.volume_token_signature_name
    VOLUME_TOKEN_DURATION         = var.volume_token_valid_for
    VOLUME_TOKEN_SIGNING_METHOD   = local.volume_token_signature_method
    CLIENT_PROXY_OIDC_ISSUER_URL  = var.client_proxy_oidc_issuer_url
  }, var.api_env_vars)

  api_db_migrator_env_vars = merge({
    POSTGRES_CONNECTION_STRING = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
  }, var.api_db_migrator_env_vars)

  client_proxy_env_vars = merge({
    ENVIRONMENT                  = var.environment
    OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:${local.otel_collector_grpc_port}"
    LOGS_COLLECTOR_ADDRESS       = "http://localhost:${local.logs_proxy_port}"
    REDIS_POOL_SIZE              = "40"
    REDIS_CLUSTER_URL            = local.redis_cluster_url
    REDIS_TLS_CA_BASE64          = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)
    REDIS_URL                    = local.redis_url
    # Used by in-cluster client-proxy to call API ResumeSandbox over gRPC.
    API_INTERNAL_GRPC_ADDRESS = "api-internal-grpc.service.consul:${var.api_internal_grpc_port}"
    LAUNCH_DARKLY_API_KEY     = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  }, var.client_proxy_env_vars)

  orchestrator_env_vars = merge({
    LOGS_COLLECTOR_ADDRESS        = "http://localhost:${local.logs_proxy_port}"
    ENVIRONMENT                   = var.environment
    ENVD_TIMEOUT                  = var.envd_timeout
    TEMPLATE_BUCKET_NAME          = module.init.fc_template_bucket_name
    OTEL_COLLECTOR_GRPC_ENDPOINT  = "localhost:${local.otel_collector_grpc_port}"
    ALLOW_SANDBOX_INTERNAL_CIDRS  = var.allow_sandbox_internal_cidrs
    CLICKHOUSE_CONNECTION_STRING  = local.clickhouse_connection_string
    REDIS_POOL_SIZE               = "10"
    REDIS_CLUSTER_URL             = local.redis_cluster_url
    REDIS_TLS_CA_BASE64           = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)
    REDIS_URL                     = local.redis_url
    GIN_MODE                      = "release"
    CONSUL_TOKEN                  = module.init.consul_acl_token_secret
    DOMAIN_NAME                   = var.domain_name
    SHARED_CHUNK_CACHE_PATH       = module.cluster.shared_chunk_cache_path
    ORCHESTRATOR_SERVICES         = "orchestrator"
    PROVIDER                      = "gcp"
    ARTIFACTS_REGISTRY_PROVIDER   = "GCP_ARTIFACTS"
    STORAGE_PROVIDER              = "GCPBucket"
    GOOGLE_SERVICE_ACCOUNT_BASE64 = ""
    GCS_GRPC_CONNECTION_POOL_SIZE = var.gcs_grpc_connection_pool_size != 0 ? tostring(var.gcs_grpc_connection_pool_size) : ""
    PERSISTENT_VOLUME_MOUNTS      = join(",", [for key, value in local.persistent_volume_types : format("%s:%s", key, value["local_mount_path"])])
    LAUNCH_DARKLY_API_KEY         = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  }, var.orchestrator_env_vars)

  template_manager_env_vars = merge({
    CONSUL_TOKEN                    = module.init.consul_acl_token_secret
    GOOGLE_SERVICE_ACCOUNT_BASE64   = module.init.google_service_account_key
    GCP_PROJECT_ID                  = var.gcp_project_id
    GCP_REGION                      = var.gcp_region
    GCP_DOCKER_REPOSITORY_NAME      = google_artifact_registry_repository.custom_environments_repository.name
    GCS_GRPC_CONNECTION_POOL_SIZE   = var.gcs_grpc_connection_pool_size != 0 ? tostring(var.gcs_grpc_connection_pool_size) : ""
    API_SECRET                      = random_password.api_secret.result
    ENVIRONMENT                     = var.environment
    DOMAIN_NAME                     = var.domain_name
    TEMPLATE_BUCKET_NAME            = module.init.fc_template_bucket_name
    BUILD_CACHE_BUCKET_NAME         = module.init.fc_build_cache_bucket_name
    OTEL_COLLECTOR_GRPC_ENDPOINT    = "localhost:${local.otel_collector_grpc_port}"
    LOGS_COLLECTOR_ADDRESS          = "http://localhost:${local.logs_proxy_port}"
    ORCHESTRATOR_SERVICES           = "template-manager"
    REDIS_POOL_SIZE                 = "10"
    SHARED_CHUNK_CACHE_PATH         = module.cluster.shared_chunk_cache_path
    CLICKHOUSE_CONNECTION_STRING    = local.clickhouse_connection_string
    DOCKERHUB_REMOTE_REPOSITORY_URL = var.remote_repository_enabled ? module.remote_repository[0].dockerhub_remote_repository_url : ""
    GIN_MODE                        = "release"
    LAUNCH_DARKLY_API_KEY           = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  }, var.template_manager_env_vars)

  docker_reverse_proxy_env_vars = merge({
    POSTGRES_CONNECTION_STRING    = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
    GOOGLE_SERVICE_ACCOUNT_BASE64 = google_service_account_key.google_service_key.private_key
    GCP_REGION                    = var.gcp_region
    GCP_PROJECT_ID                = var.gcp_project_id
    GCP_DOCKER_REPOSITORY_NAME    = google_artifact_registry_repository.custom_environments_repository.name
    DOMAIN_NAME                   = var.domain_name
  }, var.docker_reverse_proxy_env_vars)

  filestore_cleanup_env_vars = merge({
    LAUNCH_DARKLY_API_KEY = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  }, var.filestore_cleanup_env_vars)

  # Normalize additional_api_paths_handled_by_ingress to support both legacy (list of strings)
  # and new (list of objects) formats. Strings are converted to objects with paths = [string].
  normalized_api_paths_handled_by_ingress = [
    for item in var.additional_api_paths_handled_by_ingress : {
      paths       = try(item.paths, [item])
      timeout_sec = try(item.timeout_sec, null)
    }
  ]

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

  anywhere_cache = {
    enabled          = var.anywhere_cache_enabled
    admission_policy = var.anywhere_cache_admission_policy
    ttl              = var.anywhere_cache_ttl
  }
}

module "cluster" {
  source = "./nomad-cluster"

  environment = var.environment

  cloudflare_api_token_secret_name = module.init.cloudflare_api_token_secret_name
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
  additional_api_paths_handled_by_ingress = local.normalized_api_paths_handled_by_ingress

  docker_contexts_bucket_name = module.init.envs_docker_context_bucket_name
  cluster_setup_bucket_name   = module.init.cluster_setup_bucket_name
  fc_env_pipeline_bucket_name = module.init.fc_env_pipeline_bucket_name
  fc_kernels_bucket_name      = module.init.fc_kernels_bucket_name
  fc_versions_bucket_name     = module.init.fc_versions_bucket_name
  fc_busybox_bucket_name      = module.init.fc_busybox_bucket_name

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

  # ClickHouse stateful data disk
  clickhouse_stateful_disk_type    = var.clickhouse_stateful_disk_type
  clickhouse_stateful_disk_size_gb = var.clickhouse_stateful_disk_size_gb
}

module "k8s_apps" {
  source = "./k8s-apps"

  prefix         = var.prefix
  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region
  gcp_zone       = var.gcp_zone

  namespace               = "e2b"
  argocd_apps_bucket_name = module.init.argocd_apps_bucket_name
  core_repository_name    = module.init.core_repository_name

  argocd_enabled = false

  # API
  api_server_count                                       = var.api_server_count
  api_resources_cpu_count                                = var.api_resources_cpu_count
  api_resources_memory_mb                                = var.api_resources_memory_mb
  api_machine_count                                      = var.api_cluster_size
  api_node_pool                                          = var.api_node_pool
  api_port                                               = var.api_port
  api_internal_grpc_port                                 = var.api_internal_grpc_port
  api_env_vars                                           = local.api_env_vars
  api_db_migrator_env_vars                               = local.api_db_migrator_env_vars
  auth_provider_config                                   = local.auth_provider_config
  environment                                            = var.environment
  google_service_account_key                             = module.init.google_service_account_key
  api_secret                                             = random_password.api_secret.result
  custom_envs_repository_name                            = google_artifact_registry_repository.custom_environments_repository.name
  postgres_connection_string_secret_name                 = module.init.postgres_connection_string_secret_name
  postgres_read_replica_connection_string_secret_version = google_secret_manager_secret_version.postgres_read_replica_connection_string
  redis_cluster_url_secret_version                       = module.init.redis_cluster_url_secret_version
  redis_tls_ca_base64_secret_version                     = module.init.redis_tls_ca_base64_secret_version
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
  clickhouse_username              = local.clickhouse_username
  clickhouse_database              = var.clickhouse_database_name
  clickhouse_password              = random_password.clickhouse_password.result
  clickhouse_server_secret         = random_password.clickhouse_server_secret.result
  clickhouse_backups_bucket_name   = module.init.clickhouse_backups_bucket_name
  clickhouse_server_count          = var.clickhouse_cluster_size
  clickhouse_server_port           = var.clickhouse_server_service_port
  clickhouse_job_constraint_prefix = var.clickhouse_job_constraint_prefix
  clickhouse_node_pool             = var.clickhouse_node_pool

  # Ingress
  ingress_count         = var.ingress_count
  ingress_port          = var.ingress_port.port
  ingress_internal_port = var.ingress_internal_port.port

  traefik_config_files = var.traefik_config_files

  # API
  api_server_count                                       = var.api_server_count
  api_resources_cpu_count                                = var.api_resources_cpu_count
  api_resources_memory_mb                                = var.api_resources_memory_mb
  api_machine_count                                      = var.api_cluster_size
  api_node_pool                                          = var.api_node_pool
  api_port                                               = var.api_port
  api_internal_grpc_port                                 = var.api_internal_grpc_port
  api_env_vars                                           = local.api_env_vars
  api_db_migrator_env_vars                               = local.api_db_migrator_env_vars
  environment                                            = var.environment
  google_service_account_key                             = module.init.google_service_account_key
  custom_envs_repository_name                            = google_artifact_registry_repository.custom_environments_repository.name
  postgres_connection_string_secret_name                 = module.init.postgres_connection_string_secret_name
  postgres_read_replica_connection_string_secret_version = google_secret_manager_secret_version.postgres_read_replica_connection_string

  # Click Proxy
  client_proxy_count               = var.client_proxy_count
  client_proxy_resources_cpu_count = var.client_proxy_resources_cpu_count
  client_proxy_resources_memory_mb = var.client_proxy_resources_memory_mb
  client_proxy_update_max_parallel = var.client_proxy_update_max_parallel

  client_proxy_session_port = var.client_proxy_port.port
  client_proxy_health_port  = var.client_proxy_health_port.port
  client_proxy_env_vars     = local.client_proxy_env_vars

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
  otel_collector_resources_memory_mb    = var.otel_collector_resources_memory_mb
  otel_collector_resources_cpu_count    = var.otel_collector_resources_cpu_count
  otel_collector_grpc_port              = local.otel_collector_grpc_port
  logs_proxy_port                       = { name = "logs", port = local.logs_proxy_port }
  enable_otel_router_logs               = var.enable_otel_router_logs
  otel_router_http_port                 = var.otel_router_http_port
  enable_otel_router_metrics            = var.enable_otel_router_metrics
  otel_router_grpc_port                 = var.otel_router_grpc_port
  enable_gcp_telemetry_metrics          = var.enable_gcp_telemetry_metrics
  enable_gcp_telemetry_external_metrics = var.enable_gcp_telemetry_external_metrics

  # Dashboard API
  dashboard_api_count    = var.dashboard_api_count
  dashboard_api_env_vars = local.dashboard_api_env_vars

  # Docker reverse proxy
  docker_reverse_proxy_port     = var.docker_reverse_proxy_port
  docker_reverse_proxy_env_vars = local.docker_reverse_proxy_env_vars

  # Orchestrator
  orchestrator_node_pool         = var.orchestrator_node_pool
  orchestrator_port              = var.orchestrator_port
  orchestrator_proxy_port        = var.orchestrator_proxy_port
  fc_env_pipeline_bucket_name    = module.init.fc_env_pipeline_bucket_name
  default_persistent_volume_type = var.default_persistent_volume_type
  orchestrator_env_vars          = local.orchestrator_env_vars
  orchestrator_enabled           = var.orchestrator_enabled

  # Template manager
  builder_node_pool                   = var.build_node_pool
  template_manager_port               = var.template_manager_port
  template_bucket_name                = module.init.fc_template_bucket_name
  build_cache_bucket_name             = module.init.fc_build_cache_bucket_name
  template_manages_clusters_size_gt_1 = local.template_manages_clusters_size_gt_1
  dockerhub_remote_repository_url     = var.remote_repository_enabled ? module.remote_repository[0].dockerhub_remote_repository_url : ""
  template_manager_env_vars           = local.template_manager_env_vars

  # Redis
  redis_managed = var.redis_managed
  redis_port    = var.redis_port

  launch_darkly_api_key_secret_name = module.init.launch_darkly_api_key_secret_version.secret

  # Filestore
  shared_chunk_cache_path                       = module.cluster.shared_chunk_cache_path
  filestore_cache_cleanup_disk_usage_target     = var.filestore_cache_cleanup_disk_usage_target
  filestore_cache_cleanup_dry_run               = var.filestore_cache_cleanup_dry_run
  filestore_cache_cleanup_max_concurrent_stat   = var.filestore_cache_cleanup_max_concurrent_stat
  filestore_cache_cleanup_max_concurrent_scan   = var.filestore_cache_cleanup_max_concurrent_scan
  filestore_cache_cleanup_max_concurrent_delete = var.filestore_cache_cleanup_max_concurrent_delete
  filestore_cleanup_env_vars                    = local.filestore_cleanup_env_vars

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

  shard_count    = var.redis_shard_count
  engine_version = var.gcp_redis_engine_version
  node_type      = var.gcp_redis_node_type
  prefix         = var.prefix
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
