locals {
  clickhouse_connection_string = var.clickhouse_server_count > 0 ? "clickhouse://${var.clickhouse_username}:${random_password.clickhouse_password.result}@clickhouse.service.consul:${var.clickhouse_server_port.port}/${var.clickhouse_database}" : ""
  redis_url                    = trimspace(data.google_secret_manager_secret_version.redis_cluster_url.secret_data) == "" ? "redis.service.consul:${var.redis_port.port}" : ""
  redis_cluster_url            = trimspace(data.google_secret_manager_secret_version.redis_cluster_url.secret_data)
  loki_url                     = "http://loki.service.consul:${var.loki_service_port.port}"
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

  ingress_count      = var.ingress_count
  ingress_proxy_port = var.ingress_port.port

  node_pool     = var.api_node_pool
  update_stanza = var.api_machine_count > 1

  nomad_token  = var.nomad_acl_token_secret
  consul_token = var.consul_acl_token_secret
}

resource "nomad_job" "api" {
  jobspec = templatefile("${path.module}/jobs/api.hcl", {
    update_stanza = var.api_machine_count > 1
    node_pool     = var.api_node_pool
    // We use colocation 2 here to ensure that there are at least 2 nodes for API to do rolling updates.
    // It might be possible there could be problems if we are rolling updates for both API and Loki at the same time., so maybe increasing this to > 3 makes sense.
    prevent_colocation = var.api_machine_count > 2


    memory_mb = var.api_resources_memory_mb
    cpu_count = var.api_resources_cpu_count

    orchestrator_port                       = var.orchestrator_port
    otel_collector_grpc_endpoint            = "localhost:${var.otel_collector_grpc_port}"
    logs_collector_address                  = "http://localhost:${var.logs_proxy_port.port}"
    gcp_zone                                = var.gcp_zone
    port_name                               = var.api_port.name
    port_number                             = var.api_port.port
    api_grpc_port                           = var.api_grpc_port
    api_docker_image                        = data.google_artifact_registry_docker_image.api_image.self_link
    postgres_connection_string              = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
    postgres_read_replica_connection_string = trimspace(data.google_secret_manager_secret_version.postgres_read_replica_connection_string.secret_data)
    supabase_jwt_secrets                    = trimspace(data.google_secret_manager_secret_version.supabase_jwt_secrets.secret_data)
    posthog_api_key                         = trimspace(data.google_secret_manager_secret_version.posthog_api_key.secret_data)
    environment                             = var.environment
    analytics_collector_host                = trimspace(data.google_secret_manager_secret_version.analytics_collector_host.secret_data)
    analytics_collector_api_token           = trimspace(data.google_secret_manager_secret_version.analytics_collector_api_token.secret_data)
    nomad_acl_token                         = var.nomad_acl_token_secret
    admin_token                             = var.api_admin_token
    redis_url                               = local.redis_url
    redis_cluster_url                       = local.redis_cluster_url
    redis_tls_ca_base64                     = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)
    clickhouse_connection_string            = local.clickhouse_connection_string
    loki_url                                = local.loki_url
    sandbox_access_token_hash_seed          = var.sandbox_access_token_hash_seed
    db_migrator_docker_image                = data.google_artifact_registry_docker_image.db_migrator_image.self_link
    launch_darkly_api_key                   = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  })
}

resource "nomad_job" "redis" {
  count = var.redis_managed ? 0 : 1

  jobspec = templatefile("${path.module}/jobs/redis.hcl",
    {
      node_pool   = var.api_node_pool
      gcp_zone    = var.gcp_zone
      port_number = var.redis_port.port
      port_name   = var.redis_port.name
    }
  )
}

resource "nomad_job" "docker_reverse_proxy" {
  jobspec = templatefile("${path.module}/jobs/docker-reverse-proxy.hcl",
    {
      gcp_zone                      = var.gcp_zone
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

  proxy_port  = var.client_proxy_session_port
  health_port = var.client_proxy_health_port

  redis_url           = local.redis_url
  redis_cluster_url   = local.redis_cluster_url
  redis_tls_ca_base64 = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)

  image            = data.google_artifact_registry_docker_image.client_proxy_image.self_link
  api_grpc_address = "api-grpc.service.consul:${var.api_grpc_port}"

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
  name   = "orchestrator"
  bucket = var.fc_env_pipeline_bucket_name
}

data "external" "orchestrator_checksum" {
  program = ["bash", "${path.module}/scripts/checksum.sh"]

  query = {
    base64 = data.google_storage_bucket_object.orchestrator.md5hash
  }
}

locals {
  orchestrator_artifact_source = var.environment == "dev" ? "gcs::https://www.googleapis.com/storage/v1/${var.fc_env_pipeline_bucket_name}/orchestrator?version=${data.external.orchestrator_checksum.result.hex}" : "gcs::https://www.googleapis.com/storage/v1/${var.fc_env_pipeline_bucket_name}/orchestrator"
}

module "orchestrator" {
  source = "../../modules/job-orchestrator"

  provider_name = "gcp"

  node_pool  = var.orchestrator_node_pool
  port       = var.orchestrator_port
  proxy_port = var.orchestrator_proxy_port

  environment           = var.environment
  artifact_source       = local.orchestrator_artifact_source
  orchestrator_checksum = data.external.orchestrator_checksum.result.hex

  logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
  otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
  envd_timeout                 = var.envd_timeout
  template_bucket_name         = var.template_bucket_name
  allow_sandbox_internet       = var.allow_sandbox_internet
  clickhouse_connection_string = local.clickhouse_connection_string
  redis_url                    = local.redis_url
  redis_cluster_url            = local.redis_cluster_url
  redis_tls_ca_base64          = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)

  consul_token            = var.consul_acl_token_secret
  domain_name             = var.domain_name
  shared_chunk_cache_path = var.shared_chunk_cache_path
  launch_darkly_api_key   = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
}

data "google_storage_bucket_object" "template_manager" {
  name   = "template-manager"
  bucket = var.fc_env_pipeline_bucket_name
}


data "external" "template_manager" {
  program = ["bash", "${path.module}/scripts/checksum.sh"]

  query = {
    base64 = data.google_storage_bucket_object.template_manager.md5hash
  }
}

# Get current template-manager count from Nomad to preserve autoscaler-managed value
# This prevents Terraform from resetting count on job updates
# Default depends on whether scaling is enabled (min=2) or not (min=1)
data "external" "template_manager_count" {
  program = ["bash", "${path.module}/scripts/get-nomad-job-count.sh"]

  query = {
    nomad_addr  = "https://nomad.${var.domain_name}"
    nomad_token = var.nomad_acl_token_secret
    job_name    = "template-manager"
    min_count   = var.template_manages_clusters_size_gt_1 ? "2" : "1"
  }
}

resource "nomad_job" "template_manager" {
  jobspec = templatefile("${path.module}/jobs/template-manager.hcl", {
    update_stanza = var.template_manages_clusters_size_gt_1
    node_pool     = var.builder_node_pool
    current_count = tonumber(data.external.template_manager_count.result.count)

    gcp_project      = var.gcp_project_id
    gcp_region       = var.gcp_region
    gcp_zone         = var.gcp_zone
    port             = var.template_manager_port
    environment      = var.environment
    consul_acl_token = var.consul_acl_token_secret
    domain_name      = var.domain_name

    api_secret                      = var.api_secret
    bucket_name                     = var.fc_env_pipeline_bucket_name
    docker_registry                 = var.custom_envs_repository_name
    google_service_account_key      = var.google_service_account_key
    template_manager_checksum       = data.external.template_manager.result.hex
    template_bucket_name            = var.template_bucket_name
    build_cache_bucket_name         = var.build_cache_bucket_name
    otel_collector_grpc_endpoint    = "localhost:${var.otel_collector_grpc_port}"
    logs_collector_address          = "http://localhost:${var.logs_proxy_port.port}"
    orchestrator_services           = "template-manager"
    clickhouse_connection_string    = local.clickhouse_connection_string
    dockerhub_remote_repository_url = var.dockerhub_remote_repository_url
    launch_darkly_api_key           = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
    shared_chunk_cache_path         = var.shared_chunk_cache_path
  })
}

data "google_storage_bucket_object" "nomad_nodepool_apm" {
  count = var.template_manages_clusters_size_gt_1 ? 1 : 0

  name   = "nomad-nodepool-apm"
  bucket = var.fc_env_pipeline_bucket_name
}

data "external" "nomad_nodepool_apm_checksum" {
  count = var.template_manages_clusters_size_gt_1 ? 1 : 0

  program = ["bash", "${path.module}/scripts/checksum.sh"]

  query = {
    base64 = data.google_storage_bucket_object.nomad_nodepool_apm[0].md5hash
  }
}

# Nomad Autoscaler - required for template-manager dynamic scaling
resource "nomad_job" "nomad_nodepool_apm" {
  count = var.template_manages_clusters_size_gt_1 ? 1 : 0

  jobspec = templatefile("${path.module}/jobs/nomad-autoscaler.hcl", {
    node_pool                   = var.api_node_pool
    autoscaler_version          = var.nomad_autoscaler_version
    bucket_name                 = var.fc_env_pipeline_bucket_name
    nomad_token                 = var.nomad_acl_token_secret
    nomad_nodepool_apm_checksum = data.external.nomad_nodepool_apm_checksum[0].result.hex
  })
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
