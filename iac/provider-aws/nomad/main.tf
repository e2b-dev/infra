locals {
  clickhouse_connection_string = var.clickhouse_server_count > 0 ? "clickhouse://${var.clickhouse_username}:${random_password.clickhouse_password.result}@clickhouse.service.consul:${var.clickhouse_server_port.port}/${var.clickhouse_database}" : ""
  redis_url                    = trimspace(data.aws_secretsmanager_secret_version.redis_cluster_url.secret_string) == "" ? "redis.service.consul:${var.redis_port.port}" : ""
  redis_cluster_url            = trimspace(data.aws_secretsmanager_secret_version.redis_cluster_url.secret_string)
  loki_url                     = "http://loki.service.consul:${var.loki_service_port.port}"
}

# --- Nomad Provider ---
provider "nomad" {
  address      = "https://nomad.${var.domain_name}"
  secret_id    = var.nomad_acl_token_secret
  consul_token = var.consul_acl_token_secret
}

# --- Secrets Manager reads ---
data "aws_secretsmanager_secret_version" "postgres_connection_string" {
  secret_id = var.postgres_connection_string_secret_arn
}

data "aws_secretsmanager_secret_version" "postgres_read_replica_connection_string" {
  secret_id = var.postgres_read_replica_connection_string_secret_arn
}

data "aws_secretsmanager_secret_version" "supabase_jwt_secrets" {
  secret_id = var.supabase_jwt_secrets_secret_arn
}

data "aws_secretsmanager_secret_version" "posthog_api_key" {
  secret_id = var.posthog_api_key_secret_arn
}

data "aws_secretsmanager_secret_version" "analytics_collector_host" {
  secret_id = var.analytics_collector_host_secret_arn
}

data "aws_secretsmanager_secret_version" "analytics_collector_api_token" {
  secret_id = var.analytics_collector_api_token_secret_arn
}

data "aws_secretsmanager_secret_version" "launch_darkly_api_key" {
  secret_id = var.launch_darkly_api_key_secret_arn
}

data "aws_secretsmanager_secret_version" "redis_cluster_url" {
  secret_id = var.redis_cluster_url_secret_arn
}

data "aws_secretsmanager_secret_version" "redis_tls_ca_base64" {
  secret_id = var.redis_tls_ca_base64_secret_arn
}

# --- Ingress ---
module "ingress" {
  source = "../../modules/job-ingress"

  ingress_count      = var.ingress_count
  ingress_proxy_port = var.ingress_port.port

  node_pool     = var.api_node_pool
  update_stanza = var.api_machine_count > 1

  nomad_token  = var.nomad_acl_token_secret
  consul_token = var.consul_acl_token_secret

  otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
}

# --- API Job ---
resource "nomad_job" "api" {
  jobspec = templatefile("${path.module}/jobs/api.hcl", {
    update_stanza      = var.api_machine_count > 1
    node_pool          = var.api_node_pool
    prevent_colocation = var.api_machine_count > 2

    memory_mb = var.api_resources_memory_mb
    cpu_count = var.api_resources_cpu_count

    orchestrator_port                       = var.orchestrator_port
    otel_collector_grpc_endpoint            = "localhost:${var.otel_collector_grpc_port}"
    logs_collector_address                  = "http://localhost:${var.logs_proxy_port.port}"
    port_name                               = var.api_port.name
    port_number                             = var.api_port.port
    api_grpc_port                           = var.api_grpc_port
    api_docker_image                        = "${var.core_repository_url}:api-latest"
    postgres_connection_string              = data.aws_secretsmanager_secret_version.postgres_connection_string.secret_string
    postgres_read_replica_connection_string = trimspace(data.aws_secretsmanager_secret_version.postgres_read_replica_connection_string.secret_string)
    supabase_jwt_secrets                    = trimspace(data.aws_secretsmanager_secret_version.supabase_jwt_secrets.secret_string)
    posthog_api_key                         = trimspace(data.aws_secretsmanager_secret_version.posthog_api_key.secret_string)
    environment                             = var.environment
    analytics_collector_host                = trimspace(data.aws_secretsmanager_secret_version.analytics_collector_host.secret_string)
    analytics_collector_api_token           = trimspace(data.aws_secretsmanager_secret_version.analytics_collector_api_token.secret_string)
    nomad_acl_token                         = var.nomad_acl_token_secret
    admin_token                             = var.api_admin_token
    redis_url                               = local.redis_url
    redis_cluster_url                       = local.redis_cluster_url
    redis_tls_ca_base64                     = trimspace(data.aws_secretsmanager_secret_version.redis_tls_ca_base64.secret_string)
    clickhouse_connection_string            = local.clickhouse_connection_string
    loki_url                                = local.loki_url
    sandbox_access_token_hash_seed          = var.sandbox_access_token_hash_seed
    db_migrator_docker_image                = "${var.core_repository_url}:db-migrator-latest"
    launch_darkly_api_key                   = trimspace(data.aws_secretsmanager_secret_version.launch_darkly_api_key.secret_string)
  })
}

# --- Redis (self-managed, only when not using ElastiCache) ---
resource "nomad_job" "redis" {
  count = var.redis_managed ? 0 : 1

  jobspec = templatefile("${path.module}/jobs/redis.hcl",
    {
      node_pool   = var.api_node_pool
      port_number = var.redis_port.port
      port_name   = var.redis_port.name
    }
  )
}

# --- Docker Reverse Proxy ---
resource "nomad_job" "docker_reverse_proxy" {
  jobspec = templatefile("${path.module}/jobs/docker-reverse-proxy.hcl",
    {
      node_pool                 = var.api_node_pool
      image_name                = "${var.core_repository_url}:docker-reverse-proxy-latest"
      postgres_connection_string = data.aws_secretsmanager_secret_version.postgres_connection_string.secret_string
      aws_region                = var.aws_region
      ecr_repository_url        = var.core_repository_url
      port_number               = var.docker_reverse_proxy_port.port
      port_name                 = var.docker_reverse_proxy_port.name
      health_check_path         = var.docker_reverse_proxy_port.health_path
      domain_name               = var.domain_name
    }
  )
}

# --- Client Proxy ---
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
  redis_tls_ca_base64 = trimspace(data.aws_secretsmanager_secret_version.redis_tls_ca_base64.secret_string)

  image            = "${var.core_repository_url}:client-proxy-latest"
  api_grpc_address = "api-grpc.service.consul:${var.api_grpc_port}"

  otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
  logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
  launch_darkly_api_key        = trimspace(data.aws_secretsmanager_secret_version.launch_darkly_api_key.secret_string)
}

# --- Grafana secrets (create + read pattern) ---
resource "aws_secretsmanager_secret" "grafana_otlp_url" {
  name = "${var.prefix}grafana-otlp-url"
}

resource "aws_secretsmanager_secret_version" "grafana_otlp_url" {
  secret_id     = aws_secretsmanager_secret.grafana_otlp_url.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_otlp_url" {
  secret_id = aws_secretsmanager_secret.grafana_otlp_url.id
  depends_on = [aws_secretsmanager_secret_version.grafana_otlp_url]
}

resource "aws_secretsmanager_secret" "grafana_otel_collector_token" {
  name = "${var.prefix}grafana-otel-collector-token"
}

resource "aws_secretsmanager_secret_version" "grafana_otel_collector_token" {
  secret_id     = aws_secretsmanager_secret.grafana_otel_collector_token.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_otel_collector_token" {
  secret_id = aws_secretsmanager_secret.grafana_otel_collector_token.id
  depends_on = [aws_secretsmanager_secret_version.grafana_otel_collector_token]
}

resource "aws_secretsmanager_secret" "grafana_username" {
  name = "${var.prefix}grafana-username"
}

resource "aws_secretsmanager_secret_version" "grafana_username" {
  secret_id     = aws_secretsmanager_secret.grafana_username.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_username" {
  secret_id = aws_secretsmanager_secret.grafana_username.id
  depends_on = [aws_secretsmanager_secret_version.grafana_username]
}

# --- OTel Collector ---
module "otel_collector" {
  source = "../../modules/job-otel-collector"

  provider_name = "aws"

  memory_mb = var.otel_collector_resources_memory_mb
  cpu_count = var.otel_collector_resources_cpu_count

  otel_collector_grpc_port = var.otel_collector_grpc_port

  grafana_otel_collector_token = data.aws_secretsmanager_secret_version.grafana_otel_collector_token.secret_string
  grafana_otlp_url             = data.aws_secretsmanager_secret_version.grafana_otlp_url.secret_string
  grafana_username             = data.aws_secretsmanager_secret_version.grafana_username.secret_string
  consul_token                 = var.consul_acl_token_secret

  clickhouse_username = var.clickhouse_username
  clickhouse_password = random_password.clickhouse_password.result
  clickhouse_port     = var.clickhouse_server_port.port
  clickhouse_database = var.clickhouse_database
}

module "otel_collector_nomad_server" {
  source = "../../modules/job-otel-collector-nomad-server"

  provider_name = "aws"
  node_pool     = var.api_node_pool

  grafana_otel_collector_token = data.aws_secretsmanager_secret_version.grafana_otel_collector_token.secret_string
  grafana_otlp_url             = data.aws_secretsmanager_secret_version.grafana_otlp_url.secret_string
  grafana_username             = data.aws_secretsmanager_secret_version.grafana_username.secret_string
}

# --- Grafana logs secrets ---
resource "aws_secretsmanager_secret" "grafana_logs_user" {
  name = "${var.prefix}grafana-logs-user"
}

resource "aws_secretsmanager_secret_version" "grafana_logs_user" {
  secret_id     = aws_secretsmanager_secret.grafana_logs_user.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_logs_user" {
  secret_id = aws_secretsmanager_secret.grafana_logs_user.id
  depends_on = [aws_secretsmanager_secret_version.grafana_logs_user]
}

resource "aws_secretsmanager_secret" "grafana_logs_url" {
  name = "${var.prefix}grafana-logs-url"
}

resource "aws_secretsmanager_secret_version" "grafana_logs_url" {
  secret_id     = aws_secretsmanager_secret.grafana_logs_url.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_logs_url" {
  secret_id = aws_secretsmanager_secret.grafana_logs_url.id
  depends_on = [aws_secretsmanager_secret_version.grafana_logs_url]
}

resource "aws_secretsmanager_secret" "grafana_logs_collector_api_token" {
  name = "${var.prefix}grafana-api-key-logs-collector"
}

resource "aws_secretsmanager_secret_version" "grafana_logs_collector_api_token" {
  secret_id     = aws_secretsmanager_secret.grafana_logs_collector_api_token.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_logs_collector_api_token" {
  secret_id = aws_secretsmanager_secret.grafana_logs_collector_api_token.id
  depends_on = [aws_secretsmanager_secret_version.grafana_logs_collector_api_token]
}

# --- Logs Collector ---
module "logs_collector" {
  source = "../../modules/job-logs-collector"

  loki_endpoint = "http://loki.service.consul:${var.loki_service_port.port}"

  vector_health_port = var.logs_health_proxy_port.port
  vector_api_port    = var.logs_proxy_port.port

  grafana_logs_user     = trimspace(data.aws_secretsmanager_secret_version.grafana_logs_user.secret_string)
  grafana_logs_endpoint = trimspace(data.aws_secretsmanager_secret_version.grafana_logs_url.secret_string)
  grafana_api_key       = trimspace(data.aws_secretsmanager_secret_version.grafana_logs_collector_api_token.secret_string)
}

# --- Orchestrator ---
# Get orchestrator binary metadata from S3 for checksum-based change detection
data "aws_s3_object" "orchestrator" {
  bucket = var.fc_env_pipeline_bucket_name
  key    = "orchestrator"
}

locals {
  # S3 etag is the MD5 hash (without quotes) for non-multipart uploads
  orchestrator_checksum       = replace(data.aws_s3_object.orchestrator.etag, "\"", "")
  orchestrator_artifact_source = var.environment == "dev" ? "s3::https://s3.${var.aws_region}.amazonaws.com/${var.fc_env_pipeline_bucket_name}/orchestrator?versionId=${local.orchestrator_checksum}" : "s3::https://s3.${var.aws_region}.amazonaws.com/${var.fc_env_pipeline_bucket_name}/orchestrator"
}

module "orchestrator" {
  source = "../../modules/job-orchestrator"

  provider_name = "aws"

  provider_aws_config = {
    region                 = var.aws_region
    docker_repository_name = var.core_repository_url
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
  clickhouse_connection_string = local.clickhouse_connection_string
  redis_url                    = local.redis_url
  redis_cluster_url            = local.redis_cluster_url
  redis_tls_ca_base64          = trimspace(data.aws_secretsmanager_secret_version.redis_tls_ca_base64.secret_string)

  consul_token            = var.consul_acl_token_secret
  domain_name             = var.domain_name
  shared_chunk_cache_path = var.shared_chunk_cache_path
  launch_darkly_api_key   = trimspace(data.aws_secretsmanager_secret_version.launch_darkly_api_key.secret_string)
}

# --- Template Manager ---
data "aws_s3_object" "template_manager" {
  bucket = var.fc_env_pipeline_bucket_name
  key    = "template-manager"
}

locals {
  template_manager_checksum = replace(data.aws_s3_object.template_manager.etag, "\"", "")
}

# Get current template-manager count from Nomad to preserve autoscaler-managed value
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

    aws_region       = var.aws_region
    ecr_repository_url = var.core_repository_url
    port             = var.template_manager_port
    environment      = var.environment
    consul_acl_token = var.consul_acl_token_secret
    domain_name      = var.domain_name

    api_secret                      = var.api_secret
    bucket_name                     = var.fc_env_pipeline_bucket_name
    template_manager_checksum       = local.template_manager_checksum
    template_bucket_name            = var.template_bucket_name
    build_cache_bucket_name         = var.build_cache_bucket_name
    otel_collector_grpc_endpoint    = "localhost:${var.otel_collector_grpc_port}"
    logs_collector_address          = "http://localhost:${var.logs_proxy_port.port}"
    orchestrator_services           = "template-manager"
    clickhouse_connection_string    = local.clickhouse_connection_string
    dockerhub_remote_repository_url = var.dockerhub_remote_repository_url
    launch_darkly_api_key           = trimspace(data.aws_secretsmanager_secret_version.launch_darkly_api_key.secret_string)
    shared_chunk_cache_path         = var.shared_chunk_cache_path
  })
}

# --- Nomad Autoscaler ---
data "aws_s3_object" "nomad_nodepool_apm" {
  count = var.template_manages_clusters_size_gt_1 ? 1 : 0

  bucket = var.fc_env_pipeline_bucket_name
  key    = "nomad-nodepool-apm"
}

resource "nomad_job" "nomad_nodepool_apm" {
  count = var.template_manages_clusters_size_gt_1 ? 1 : 0

  jobspec = templatefile("${path.module}/jobs/nomad-autoscaler.hcl", {
    node_pool                   = var.api_node_pool
    autoscaler_version          = var.nomad_autoscaler_version
    bucket_name                 = var.fc_env_pipeline_bucket_name
    aws_region                  = var.aws_region
    nomad_token                 = var.nomad_acl_token_secret
    nomad_nodepool_apm_checksum = replace(data.aws_s3_object.nomad_nodepool_apm[0].etag, "\"", "")
  })
}

# --- Loki ---
module "loki" {
  source = "../../modules/job-loki"

  provider_name = "aws"
  aws_region    = var.aws_region

  node_pool = var.loki_machine_count > 0 ? var.loki_node_pool : var.api_node_pool

  prevent_colocation = var.api_machine_count > 2
  bucket_name        = var.loki_bucket_name

  memory_mb = var.loki_resources_memory_mb
  cpu_count = var.loki_resources_cpu_count
  loki_port = var.loki_service_port.port

  loki_use_v13_schema_from = var.loki_use_v13_schema_from
}

# --- ClickHouse ---
resource "random_password" "clickhouse_password" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "clickhouse_password" {
  name = "${var.prefix}clickhouse-password"
}

resource "aws_secretsmanager_secret_version" "clickhouse_password_value" {
  secret_id     = aws_secretsmanager_secret.clickhouse_password.id
  secret_string = random_password.clickhouse_password.result
}

resource "random_password" "clickhouse_server_secret" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "clickhouse_server_secret" {
  name = "${var.prefix}clickhouse-server-secret"
}

resource "aws_secretsmanager_secret_version" "clickhouse_server_secret_value" {
  secret_id     = aws_secretsmanager_secret.clickhouse_server_secret.id
  secret_string = random_password.clickhouse_server_secret.result
}

module "clickhouse" {
  source = "../../modules/job-clickhouse"

  provider_name = "aws"
  aws_region    = var.aws_region

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

  # Backup - S3 backup uses IAM role, no explicit credentials needed
  backup_bucket = var.clickhouse_backups_bucket_name

  # Migrator
  clickhouse_migrator_image = "${var.core_repository_url}:clickhouse-migrator-latest"
}

# --- EFS/NFS Cache Cleanup ---
data "aws_s3_object" "clean_nfs_cache" {
  count = var.shared_chunk_cache_path != "" ? 1 : 0

  bucket = var.fc_env_pipeline_bucket_name
  key    = "clean-nfs-cache"
}

resource "nomad_job" "efs_cleanup" {
  count = var.shared_chunk_cache_path != "" ? 1 : 0

  jobspec = templatefile("${path.module}/jobs/clean-nfs-cache.hcl", {
    node_pool                    = var.orchestrator_node_pool
    environment                  = var.environment
    aws_region                   = var.aws_region
    bucket_name                  = var.fc_env_pipeline_bucket_name
    clean_nfs_cache_checksum     = replace(data.aws_s3_object.clean_nfs_cache[0].etag, "\"", "")
    nfs_cache_mount_path         = var.shared_chunk_cache_path
    otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
    dry_run                      = var.filestore_cache_cleanup_dry_run
    max_disk_usage_target        = var.filestore_cache_cleanup_disk_usage_target
    files_per_loop               = var.filestore_cache_cleanup_files_per_loop
    deletions_per_loop           = var.filestore_cache_cleanup_deletions_per_loop
    max_concurrent_stat          = var.filestore_cache_cleanup_max_concurrent_stat
    max_concurrent_scan          = var.filestore_cache_cleanup_max_concurrent_scan
    max_concurrent_delete        = var.filestore_cache_cleanup_max_concurrent_delete
    max_retries                  = var.filestore_cache_cleanup_max_retries
    launch_darkly_api_key        = trimspace(data.aws_secretsmanager_secret_version.launch_darkly_api_key.secret_string)
  })
}
