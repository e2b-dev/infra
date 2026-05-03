locals {
  clickhouse_connection_string            = var.clickhouse_server_count > 0 ? "clickhouse://${var.clickhouse_username}:${random_password.clickhouse_password.result}@clickhouse.service.consul:${var.clickhouse_server_port.port}/${var.clickhouse_database}" : ""
  redis_url                               = trimspace(data.google_secret_manager_secret_version.redis_cluster_url.secret_data) == "" ? "redis.service.consul:${var.redis_port.port}" : ""
  redis_cluster_url                       = trimspace(data.google_secret_manager_secret_version.redis_cluster_url.secret_data)
  loki_url                                = "http://loki.service.consul:${var.loki_service_port.port}"
  enable_billing_http_team_provision_sink = var.enable_billing_http_team_provision_sink
  dashboard_api_billing_server_url        = local.enable_billing_http_team_provision_sink ? trimspace(data.google_secret_manager_secret_version.billing_server_url[0].secret_data) : ""
  dashboard_api_billing_server_api_token  = local.enable_billing_http_team_provision_sink ? trimspace(data.google_secret_manager_secret_version.billing_server_api_token[0].secret_data) : ""
}

# API
data "google_secret_manager_secret_version" "postgres_connection_string" {
  secret = var.postgres_connection_string_secret_name
}

data "google_secret_manager_secret_version" "postgres_read_replica_connection_string" {
  secret = var.postgres_read_replica_connection_string_secret_version.secret
}

data "google_secret_manager_secret_version" "supabase_jwt_secrets" {
  secret = var.supabase_jwt_secrets_secret_name
}

data "google_secret_manager_secret_version" "posthog_api_key" {
  secret = var.posthog_api_key_secret_name
}

data "google_secret_manager_secret_version" "api_admin_token" {
  secret = var.api_admin_token_secret_name
}

data "google_secret_manager_secret_version" "dashboard_api_admin_token" {
  secret = var.dashboard_api_admin_token_secret_name
}

data "google_secret_manager_secret_version" "supabase_db_connection_string" {
  secret = var.supabase_db_connection_string_secret_version.secret
}

# Telemetry
data "google_secret_manager_secret_version" "analytics_collector_host" {
  secret = var.analytics_collector_host_secret_name
}

data "google_secret_manager_secret_version" "analytics_collector_api_token" {
  secret = var.analytics_collector_api_token_secret_name
}

data "google_secret_manager_secret_version" "launch_darkly_api_key" {
  secret = var.launch_darkly_api_key_secret_name
}

data "google_secret_manager_secret_version" "billing_server_api_token" {
  count = local.enable_billing_http_team_provision_sink ? 1 : 0

  project = var.gcp_project_id
  secret  = "${var.prefix}billing-server-api-token"
}

data "google_secret_manager_secret_version" "billing_server_url" {
  count = local.enable_billing_http_team_provision_sink ? 1 : 0

  project = var.gcp_project_id
  secret  = "${var.prefix}billing-server-url"
}

provider "nomad" {
  address      = "https://nomad.${var.domain_name}"
  secret_id    = var.nomad_acl_token_secret
  consul_token = var.consul_acl_token_secret
}

data "google_secret_manager_secret_version" "redis_cluster_url" {
  secret = var.redis_cluster_url_secret_version.secret
}

data "google_secret_manager_secret_version" "redis_tls_ca_base64" {
  secret = var.redis_tls_ca_base64_secret_version.secret
}

module "ingress" {
  source = "../../modules/job-ingress"

  ingress_count        = var.ingress_count
  ingress_proxy_port   = var.ingress_port.port
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

  domain_name                             = var.domain_name
  orchestrator_port                       = var.orchestrator_port
  otel_collector_grpc_endpoint            = "localhost:${var.otel_collector_grpc_port}"
  logs_collector_address                  = "http://localhost:${var.logs_proxy_port.port}"
  port_name                               = var.api_port.name
  port_number                             = var.api_port.port
  api_internal_grpc_port                  = var.api_internal_grpc_port
  internal_tls_ca_pool                    = var.api_internal_tls_ca_pool
  internal_tls_ca_authority               = var.api_internal_tls_ca_authority
  internal_tls_dns_name                   = var.api_internal_tls_dns_name
  internal_tls_cert_id_prefix             = var.api_internal_tls_cert_id_prefix
  api_docker_image                        = data.google_artifact_registry_docker_image.api_image.self_link
  postgres_connection_string              = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
  postgres_read_replica_connection_string = trimspace(data.google_secret_manager_secret_version.postgres_read_replica_connection_string.secret_data)
  supabase_jwt_secrets                    = trimspace(data.google_secret_manager_secret_version.supabase_jwt_secrets.secret_data)
  posthog_api_key                         = trimspace(data.google_secret_manager_secret_version.posthog_api_key.secret_data)
  environment                             = var.environment
  analytics_collector_host                = trimspace(data.google_secret_manager_secret_version.analytics_collector_host.secret_data)
  analytics_collector_api_token           = trimspace(data.google_secret_manager_secret_version.analytics_collector_api_token.secret_data)
  nomad_acl_token                         = var.nomad_acl_token_secret
  admin_token                             = trimspace(data.google_secret_manager_secret_version.api_admin_token.secret_data)
  redis_url                               = local.redis_url
  redis_cluster_url                       = local.redis_cluster_url
  redis_tls_ca_base64                     = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)
  clickhouse_connection_string            = local.clickhouse_connection_string
  loki_url                                = local.loki_url
  sandbox_access_token_hash_seed          = var.sandbox_access_token_hash_seed
  sandbox_storage_backend                 = var.sandbox_storage_backend
  db_max_open_connections                 = var.db_max_open_connections
  db_min_idle_connections                 = var.db_min_idle_connections
  auth_db_max_open_connections            = var.auth_db_max_open_connections
  auth_db_min_idle_connections            = var.auth_db_min_idle_connections
  db_migrator_docker_image                = data.google_artifact_registry_docker_image.db_migrator_image.self_link
  launch_darkly_api_key                   = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  default_persistent_volume_type          = var.default_persistent_volume_type

  job_env_vars = {
    VOLUME_TOKEN_ISSUER           = var.volume_token_issuer
    VOLUME_TOKEN_SIGNING_KEY      = var.volume_token_signing_key
    VOLUME_TOKEN_SIGNING_KEY_NAME = var.volume_token_signing_key_name
    VOLUME_TOKEN_DURATION         = var.volume_token_duration
    VOLUME_TOKEN_SIGNING_METHOD   = var.volume_token_signing_method
    CLIENT_PROXY_OIDC_ISSUER_URL  = var.client_proxy_oidc_issuer_url
  }
}

module "dashboard_api" {
  source = "../../modules/job-dashboard-api"
  count  = var.dashboard_api_count > 0 ? 1 : 0

  count_instances = var.dashboard_api_count
  node_pool       = var.api_node_pool
  update_stanza   = var.dashboard_api_count > 1
  environment     = var.environment

  image = data.google_artifact_registry_docker_image.dashboard_api_image[0].self_link

  admin_token                             = trimspace(data.google_secret_manager_secret_version.dashboard_api_admin_token.secret_data)
  postgres_connection_string              = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
  auth_db_connection_string               = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
  auth_db_read_replica_connection_string  = trimspace(data.google_secret_manager_secret_version.postgres_read_replica_connection_string.secret_data)
  supabase_db_connection_string           = trimspace(data.google_secret_manager_secret_version.supabase_db_connection_string.secret_data)
  clickhouse_connection_string            = local.clickhouse_connection_string
  supabase_jwt_secrets                    = trimspace(data.google_secret_manager_secret_version.supabase_jwt_secrets.secret_data)
  redis_url                               = local.redis_url
  redis_cluster_url                       = local.redis_cluster_url
  redis_tls_ca_base64                     = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)
  enable_auth_user_sync_background_worker = var.enable_auth_user_sync_background_worker
  enable_billing_http_team_provision_sink = var.enable_billing_http_team_provision_sink
  billing_server_url                      = local.dashboard_api_billing_server_url
  billing_server_api_token                = local.dashboard_api_billing_server_api_token

  otel_collector_grpc_port = var.otel_collector_grpc_port
  logs_proxy_port          = var.logs_proxy_port
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
      node_pool                     = var.api_node_pool
      image_name                    = data.google_artifact_registry_docker_image.docker_reverse_proxy_image.self_link
      postgres_connection_string    = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
      google_service_account_secret = var.docker_reverse_proxy_service_account_key
      port_number                   = var.docker_reverse_proxy_port.port
      port_name                     = var.docker_reverse_proxy_port.name
      health_check_path             = var.docker_reverse_proxy_port.health_path
      domain_name                   = var.domain_name
      gcp_project_id                = var.gcp_project_id
      gcp_region                    = var.gcp_region
      docker_registry               = var.custom_envs_repository_name
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

  node_pool   = var.api_node_pool
  environment = var.environment

  proxy_port                  = var.client_proxy_session_port
  proxy_tls_port              = var.client_proxy_tls_session_port
  health_port                 = var.client_proxy_health_port
  internal_tls_ca_pool        = var.client_proxy_internal_tls_ca_pool
  internal_tls_ca_authority   = var.client_proxy_internal_tls_ca_authority
  internal_tls_dns_name       = var.client_proxy_internal_tls_dns_name
  internal_tls_cert_id_prefix = var.client_proxy_internal_tls_cert_id_prefix

  redis_url                 = local.redis_url
  redis_cluster_url         = local.redis_cluster_url
  redis_tls_ca_base64       = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)
  image                     = data.google_artifact_registry_docker_image.client_proxy_image.self_link
  api_internal_grpc_address = "api-internal-grpc.service.consul:${var.api_internal_grpc_port}"

  otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
  logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
  launch_darkly_api_key        = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
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

  clickhouse_username = var.clickhouse_username
  clickhouse_password = random_password.clickhouse_password.result
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

  provider_name = "gcp"
  provider_gcp_config = {
    gcs_grpc_connection_pool_size = var.gcs_grpc_connection_pool_size
  }

  node_pool  = var.orchestrator_node_pool
  port       = var.orchestrator_port
  proxy_port = var.orchestrator_proxy_port

  environment           = var.environment
  artifact_source       = local.orchestrator_artifact_source
  orchestrator_checksum = local.orchestrator_checksum

  logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
  otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
  envd_timeout                 = var.envd_timeout
  template_bucket_name         = var.template_bucket_name
  allow_sandbox_internet       = var.allow_sandbox_internet
  allow_sandbox_internal_cidrs = var.allow_sandbox_internal_cidrs
  clickhouse_connection_string = local.clickhouse_connection_string
  redis_url                    = local.redis_url
  redis_cluster_url            = local.redis_cluster_url
  redis_tls_ca_base64          = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)
  persistent_volume_mounts     = var.persistent_volume_mounts

  consul_token            = var.consul_acl_token_secret
  domain_name             = var.domain_name
  shared_chunk_cache_path = var.shared_chunk_cache_path
  launch_darkly_api_key   = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)

  job_env_vars = var.orchestrator_env_vars
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

  provider_name = "gcp"
  provider_gcp_config = {
    service_account_key           = var.google_service_account_key
    project_id                    = var.gcp_project_id
    region                        = var.gcp_region
    docker_registry               = var.custom_envs_repository_name
    gcs_grpc_connection_pool_size = var.gcs_grpc_connection_pool_size
  }

  update_stanza = var.template_manages_clusters_size_gt_1
  node_pool     = var.builder_node_pool

  port             = var.template_manager_port
  environment      = var.environment
  consul_acl_token = var.consul_acl_token_secret
  domain_name      = var.domain_name

  api_secret                      = var.api_secret
  artifact_source                 = local.template_manager_artifact_source
  template_bucket_name            = var.template_bucket_name
  build_cache_bucket_name         = var.build_cache_bucket_name
  otel_collector_grpc_endpoint    = "localhost:${var.otel_collector_grpc_port}"
  logs_collector_address          = "http://localhost:${var.logs_proxy_port.port}"
  clickhouse_connection_string    = local.clickhouse_connection_string
  dockerhub_remote_repository_url = var.dockerhub_remote_repository_url
  launch_darkly_api_key           = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  shared_chunk_cache_path         = var.shared_chunk_cache_path

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

# Create only one user for simplicity now, will separate users in following PRs
resource "random_password" "clickhouse_password" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "clickhouse_password" {
  secret_id = "${var.prefix}clickhouse-password"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "clickhouse_password_value" {
  secret = google_secret_manager_secret.clickhouse_password.id

  secret_data = random_password.clickhouse_password.result
}

resource "random_password" "clickhouse_server_secret" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "clickhouse_server_secret" {
  secret_id = "${var.prefix}clickhouse-server-secret"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "clickhouse_server_secret_value" {
  secret = google_secret_manager_secret.clickhouse_server_secret.id

  secret_data = random_password.clickhouse_server_secret.result
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
  server_secret = random_password.clickhouse_server_secret.result
  cpu_count     = var.clickhouse_resources_cpu_count
  memory_mb     = var.clickhouse_resources_memory_mb

  clickhouse_database = var.clickhouse_database
  clickhouse_username = var.clickhouse_username
  clickhouse_password = random_password.clickhouse_password.result
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
    deletions_per_loop           = var.filestore_cache_cleanup_deletions_per_loop
    files_per_loop               = var.filestore_cache_cleanup_files_per_loop
    max_concurrent_stat          = var.filestore_cache_cleanup_max_concurrent_stat
    max_concurrent_scan          = var.filestore_cache_cleanup_max_concurrent_scan
    max_concurrent_delete        = var.filestore_cache_cleanup_max_concurrent_delete
    max_retries                  = var.filestore_cache_cleanup_max_retries
    otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
    launch_darkly_api_key        = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  })
}
