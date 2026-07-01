locals {
  clickhouse_connection_string = var.clickhouse_server_count > 0 ? "clickhouse://${var.clickhouse_username}:${var.clickhouse_password}@clickhouse.service.consul:${var.clickhouse_server_port.port}/${var.clickhouse_database}" : ""

  docker_reverse_proxy_env_vars = {
    for key, value in var.docker_reverse_proxy_env_vars : key => trimspace(value)
    if value != null && try(trimspace(value), "") != ""
  }

  filestore_cleanup_env_vars = {
    for key, value in var.filestore_cleanup_env_vars : key => trimspace(value)
    if value != null && try(trimspace(value), "") != ""
  }
}

# API
data "google_secret_manager_secret_version" "postgres_connection_string" {
  secret = var.postgres_connection_string_secret_name
}

data "google_secret_manager_secret_version" "postgres_read_replica_connection_string" {
  secret = var.postgres_read_replica_connection_string_secret_version.secret
}

# Telemetry
data "google_secret_manager_secret_version" "launch_darkly_api_key" {
  secret = var.launch_darkly_api_key_secret_name
}

provider "nomad" {
  address      = "https://nomad.${var.domain_name}"
  secret_id    = var.nomad_acl_token_secret
  consul_token = var.consul_acl_token_secret
}

// Turn on memory oversubscription
// Its already set up in Nomad server config, but from there its taked only for newly created clusters so we need to make sure its apply here to existing.
resource "nomad_scheduler_config" "config" {
  memory_oversubscription_enabled = true
}

module "ingress" {
  source = "../../modules/job-ingress"

  ingress_count         = var.ingress_count
  ingress_port          = var.ingress_port
  ingress_internal_port = var.ingress_internal_port

  traefik_config_files = var.traefik_config_files

  node_pool     = var.api_node_pool
  update_stanza = var.api_machine_count > 1

  nomad_token  = var.nomad_acl_token_secret
  consul_token = var.consul_acl_token_secret

  otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
}

module "api" {
  source = "../../modules/job-api"

  update_stanza = var.api_machine_count > 1
  node_pool     = var.api_node_pool
  // We use colocation 2 here to ensure that there are at least 2 nodes for API to do rolling updates.
  // It might be possible there could be problems if we are rolling updates for both API and Loki at the same time., so maybe increasing this to > 3 makes sense.
  prevent_colocation = var.api_machine_count > 2
  count_instances    = var.api_server_count

  memory_mb = var.api_resources_memory_mb
  cpu_count = var.api_resources_cpu_count

  port_name                = var.api_port.name
  port_number              = var.api_port.port
  api_internal_grpc_port   = var.api_internal_grpc_port
  api_docker_image         = data.google_artifact_registry_docker_image.api_image.self_link
  db_migrator_docker_image = data.google_artifact_registry_docker_image.db_migrator_image.self_link
  job_env_vars             = var.api_env_vars
  db_migrator_env_vars     = var.api_db_migrator_env_vars
}

module "dashboard_api" {
  source = "../../modules/job-dashboard-api"
  count  = var.dashboard_api_count > 0 ? 1 : 0

  count_instances = var.dashboard_api_count
  node_pool       = var.api_node_pool
  update_stanza   = var.dashboard_api_count > 1

  image = data.google_artifact_registry_docker_image.dashboard_api_image[0].self_link

  job_env_vars = var.dashboard_api_env_vars
}

module "redis" {
  source = "../../modules/job-redis"
  count  = var.redis_managed ? 0 : 1

  node_pool   = var.api_node_pool
  port_number = var.redis_port.port
  port_name   = var.redis_port.name
}

resource "nomad_job" "docker_reverse_proxy" {
  jobspec = templatefile("${path.module}/jobs/docker-reverse-proxy.hcl",
    {
      node_pool         = var.api_node_pool
      image_name        = data.google_artifact_registry_docker_image.docker_reverse_proxy_image.self_link
      port_number       = var.docker_reverse_proxy_port.port
      port_name         = var.docker_reverse_proxy_port.name
      health_check_path = var.docker_reverse_proxy_port.health_path
      job_env_vars      = local.docker_reverse_proxy_env_vars
    }
  )
}

module "client_proxy" {
  source = "../../modules/job-client-proxy"

  update_stanza                    = var.api_machine_count > 1
  client_proxy_count               = var.client_proxy_count
  client_proxy_cpu_count           = var.client_proxy_resources_cpu_count
  client_proxy_memory_mb           = var.client_proxy_resources_memory_mb
  client_proxy_update_max_parallel = var.client_proxy_update_max_parallel

  node_pool = var.api_node_pool

  proxy_port  = var.client_proxy_session_port
  health_port = var.client_proxy_health_port

  image        = data.google_artifact_registry_docker_image.client_proxy_image.self_link
  job_env_vars = var.client_proxy_env_vars
}

# grafana otel collector url
resource "google_secret_manager_secret" "grafana_otlp_url" {
  secret_id = "${var.prefix}grafana-otlp-url"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_otlp_url" {
  secret      = google_secret_manager_secret.grafana_otlp_url.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

data "google_secret_manager_secret_version" "grafana_otlp_url" {
  secret = google_secret_manager_secret.grafana_otlp_url.name

  depends_on = [google_secret_manager_secret_version.grafana_otlp_url]
}


# grafana otel collector token
resource "google_secret_manager_secret" "grafana_otel_collector_token" {
  secret_id = "${var.prefix}grafana-otel-collector-token"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_otel_collector_token" {
  secret      = google_secret_manager_secret.grafana_otel_collector_token.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

data "google_secret_manager_secret_version" "grafana_otel_collector_token" {
  secret = google_secret_manager_secret.grafana_otel_collector_token.name

  depends_on = [google_secret_manager_secret_version.grafana_otel_collector_token]
}


# grafana username
resource "google_secret_manager_secret" "grafana_username" {
  secret_id = "${var.prefix}grafana-username"

  replication {
    auto {}
  }
}


resource "google_secret_manager_secret_version" "grafana_username" {
  secret      = google_secret_manager_secret.grafana_username.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

data "google_secret_manager_secret_version" "grafana_username" {
  secret = google_secret_manager_secret.grafana_username.name

  depends_on = [google_secret_manager_secret_version.grafana_username]
}

module "otel_collector" {
  source = "../../modules/job-otel-collector"

  provider_name = "gcp"

  memory_mb = var.otel_collector_resources_memory_mb
  cpu_count = var.otel_collector_resources_cpu_count

  otel_collector_grpc_port = var.otel_collector_grpc_port

  grafana_otel_collector_token = data.google_secret_manager_secret_version.grafana_otel_collector_token.secret_data
  grafana_otlp_url             = data.google_secret_manager_secret_version.grafana_otlp_url.secret_data
  grafana_username             = data.google_secret_manager_secret_version.grafana_username.secret_data
  consul_token                 = var.consul_acl_token_secret

  enable_otel_router_metrics = var.enable_otel_router_metrics
  otel_router_grpc_port      = var.otel_router_grpc_port

  enable_gcp_telemetry_metrics          = var.enable_gcp_telemetry_metrics
  enable_gcp_telemetry_external_metrics = var.enable_gcp_telemetry_external_metrics
  gcp_telemetry_project_id              = var.gcp_project_id

  clickhouse_username = var.clickhouse_username
  clickhouse_password = var.clickhouse_password
  clickhouse_port     = var.clickhouse_server_port.port
  clickhouse_database = var.clickhouse_database
}

module "otel_collector_nomad_server" {
  source = "../../modules/job-otel-collector-nomad-server"

  provider_name = "gcp"
  node_pool     = var.api_node_pool

  grafana_otel_collector_token = data.google_secret_manager_secret_version.grafana_otel_collector_token.secret_data
  grafana_otlp_url             = data.google_secret_manager_secret_version.grafana_otlp_url.secret_data
  grafana_username             = data.google_secret_manager_secret_version.grafana_username.secret_data

  enable_gcp_telemetry_metrics = var.enable_gcp_telemetry_metrics
  gcp_telemetry_project_id     = var.gcp_project_id
}


resource "google_secret_manager_secret" "grafana_logs_user" {
  secret_id = "${var.prefix}grafana-logs-user"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_logs_user" {
  secret      = google_secret_manager_secret.grafana_logs_user.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

data "google_secret_manager_secret_version" "grafana_logs_user" {
  secret = google_secret_manager_secret.grafana_logs_user.name

  depends_on = [google_secret_manager_secret_version.grafana_logs_user]
}

resource "google_secret_manager_secret" "grafana_logs_url" {
  secret_id = "${var.prefix}grafana-logs-url"

  replication {
    auto {}
  }

}

resource "google_secret_manager_secret_version" "grafana_logs_url" {
  secret      = google_secret_manager_secret.grafana_logs_url.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

data "google_secret_manager_secret_version" "grafana_logs_url" {
  secret = google_secret_manager_secret.grafana_logs_url.name

  depends_on = [google_secret_manager_secret_version.grafana_logs_url]
}


resource "google_secret_manager_secret" "grafana_logs_collector_api_token" {
  secret_id = "${var.prefix}grafana-api-key-logs-collector"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_logs_collector_api_token" {
  secret      = google_secret_manager_secret.grafana_logs_collector_api_token.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

data "google_secret_manager_secret_version" "grafana_logs_collector_api_token" {
  secret = google_secret_manager_secret.grafana_logs_collector_api_token.name

  depends_on = [google_secret_manager_secret_version.grafana_logs_collector_api_token]
}

module "logs_collector" {
  source = "../../modules/job-logs-collector"

  loki_endpoint = "http://loki.service.consul:${var.loki_service_port.port}"

  enable_otel_router_logs = var.enable_otel_router_logs
  otel_router_http_port   = var.otel_router_http_port

  vector_health_port = var.logs_health_proxy_port.port
  vector_api_port    = var.logs_proxy_port.port

  grafana_logs_user     = trimspace(data.google_secret_manager_secret_version.grafana_logs_user.secret_data)
  grafana_logs_endpoint = trimspace(data.google_secret_manager_secret_version.grafana_logs_url.secret_data)
  grafana_api_key       = trimspace(data.google_secret_manager_secret_version.grafana_logs_collector_api_token.secret_data)
}

data "google_storage_bucket_object" "orchestrator" {
  count  = var.orchestrator_enabled ? 1 : 0
  name   = "orchestrator"
  bucket = var.fc_env_pipeline_bucket_name
}

locals {
  orchestrator_checksum        = var.orchestrator_enabled ? data.google_storage_bucket_object.orchestrator[0].generation : ""
  orchestrator_artifact_source = var.orchestrator_enabled ? "gcs::https://www.googleapis.com/storage/v1/${var.fc_env_pipeline_bucket_name}/orchestrator?version=${local.orchestrator_checksum}" : ""
}

module "orchestrator" {
  count = var.orchestrator_enabled ? 1 : 0

  source = "../../modules/job-orchestrator"

  node_pool  = var.orchestrator_node_pool
  port       = var.orchestrator_port
  proxy_port = var.orchestrator_proxy_port

  environment           = var.environment
  artifact_source       = local.orchestrator_artifact_source
  orchestrator_checksum = local.orchestrator_checksum
  job_env_vars          = var.orchestrator_env_vars
}

data "google_storage_bucket_object" "template_manager" {
  name   = "template-manager"
  bucket = var.fc_env_pipeline_bucket_name
}

locals {
  template_manager_artifact_source = "gcs::https://www.googleapis.com/storage/v1/${var.fc_env_pipeline_bucket_name}/template-manager?version=${data.google_storage_bucket_object.template_manager.generation}"
}

module "template_manager" {
  source = "../../modules/job-template-manager"

  update_stanza = var.template_manages_clusters_size_gt_1
  node_pool     = var.builder_node_pool

  port = var.template_manager_port

  artifact_source = local.template_manager_artifact_source
  job_env_vars    = var.template_manager_env_vars

  nomad_addr  = "https://nomad.${var.domain_name}"
  nomad_token = var.nomad_acl_token_secret
}

data "google_storage_bucket_object" "nomad_nodepool_apm" {
  count = var.template_manages_clusters_size_gt_1 ? 1 : 0

  name   = "nomad-nodepool-apm"
  bucket = var.fc_env_pipeline_bucket_name
}

module "template_manager_autoscaler" {
  source = "../../modules/job-template-manager-autoscaler"
  count  = var.template_manages_clusters_size_gt_1 ? 1 : 0

  node_pool                  = var.api_node_pool
  autoscaler_version         = var.nomad_autoscaler_version
  nomad_token                = var.nomad_acl_token_secret
  apm_plugin_artifact_source = "gcs::https://www.googleapis.com/storage/v1/${var.fc_env_pipeline_bucket_name}/nomad-nodepool-apm?version=${data.google_storage_bucket_object.nomad_nodepool_apm[0].generation}"
}

module "loki" {
  source = "../../modules/job-loki"

  provider_name = "gcp"

  node_pool = var.loki_machine_count > 0 ? var.loki_node_pool : var.api_node_pool

  // We use colocation 2 here to ensure that there are at least 2 nodes for API to do rolling updates.
  // It might be possible there could be problems if we are rolling updates for both API and Loki at the same time., so maybe increasing this to > 3 makes sense.
  prevent_colocation = var.api_machine_count > 2
  bucket_name        = var.loki_bucket_name

  memory_mb = var.loki_resources_memory_mb
  cpu_count = var.loki_resources_cpu_count
  loki_port = var.loki_service_port.port

  loki_use_v13_schema_from = var.loki_use_v13_schema_from
}

resource "google_secret_manager_secret" "clickhouse_password" {
  secret_id = "${var.prefix}clickhouse-password"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "clickhouse_password_value" {
  secret = google_secret_manager_secret.clickhouse_password.id

  secret_data = var.clickhouse_password
}

resource "google_secret_manager_secret" "clickhouse_server_secret" {
  secret_id = "${var.prefix}clickhouse-server-secret"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "clickhouse_server_secret_value" {
  secret = google_secret_manager_secret.clickhouse_server_secret.id

  secret_data = var.clickhouse_server_secret
}

resource "google_service_account" "clickhouse_service_account" {
  account_id   = "${var.prefix}clickhouse-service-account"
  display_name = "${var.prefix}clickhouse-service-account"
}

resource "google_storage_bucket_iam_member" "clickhouse_service_account_iam" {
  bucket = var.clickhouse_backups_bucket_name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.clickhouse_service_account.email}"
}

resource "google_storage_hmac_key" "clickhouse_hmac_key" {
  service_account_email = google_service_account.clickhouse_service_account.email
}

resource "google_service_account_key" "clickhouse_service_account_key" {
  service_account_id = google_service_account.clickhouse_service_account.id
}

module "clickhouse" {
  source = "../../modules/job-clickhouse"

  provider_name = "gcp"

  node_pool             = var.clickhouse_node_pool
  job_constraint_prefix = var.clickhouse_job_constraint_prefix
  server_count          = var.clickhouse_server_count

  # Server
  server_secret = var.clickhouse_server_secret
  cpu_count     = var.clickhouse_resources_cpu_count
  memory_mb     = var.clickhouse_resources_memory_mb

  clickhouse_database = var.clickhouse_database
  clickhouse_username = var.clickhouse_username
  clickhouse_password = var.clickhouse_password
  clickhouse_port     = var.clickhouse_server_port.port

  clickhouse_metrics_port = var.clickhouse_metrics_port
  otel_exporter_endpoint  = "http://localhost:${var.otel_collector_grpc_port}"

  # Backup
  backup_bucket                = var.clickhouse_backups_bucket_name
  gcs_credentials_json_encoded = google_service_account_key.clickhouse_service_account_key.private_key

  # Migrator
  clickhouse_migrator_image = data.google_artifact_registry_docker_image.clickhouse_migrator_image.self_link
}

data "google_storage_bucket_object" "filestore_cleanup" {
  name   = "clean-nfs-cache"
  bucket = var.fc_env_pipeline_bucket_name
}

locals {
  clean_nfs_cache_artifact_source = "gcs::https://www.googleapis.com/storage/v1/${var.fc_env_pipeline_bucket_name}/clean-nfs-cache?version=${data.google_storage_bucket_object.filestore_cleanup.generation}"
}

resource "nomad_job" "clean_nfs_cache" {
  count = var.shared_chunk_cache_path != "" ? 1 : 0

  jobspec = templatefile("${path.module}/jobs/clean-nfs-cache.hcl", {
    node_pool                    = var.builder_node_pool
    artifact_source              = local.clean_nfs_cache_artifact_source
    nfs_cache_mount_path         = var.shared_chunk_cache_path
    max_disk_usage_target        = var.filestore_cache_cleanup_disk_usage_target
    dry_run                      = var.filestore_cache_cleanup_dry_run
    max_concurrent_stat          = var.filestore_cache_cleanup_max_concurrent_stat
    max_concurrent_scan          = var.filestore_cache_cleanup_max_concurrent_scan
    max_concurrent_delete        = var.filestore_cache_cleanup_max_concurrent_delete
    otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
    job_env_vars                 = local.filestore_cleanup_env_vars
  })
}
