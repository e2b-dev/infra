locals {
  clickhouse_connection_string = var.clickhouse_cluster_size > 0 ? "clickhouse://${var.clickhouse_username}:${var.clickhouse_password}@clickhouse.service.consul:${var.clickhouse_port}/${var.clickhouse_database}" : ""
}

data "aws_ecr_image" "api" {
  repository_name = var.api_repository_name
  image_tag       = "latest"
}

data "aws_ecr_image" "db_migrator" {
  repository_name = var.db_migrator_repository_name
  image_tag       = "latest"
}

data "aws_ecr_image" "client_proxy" {
  repository_name = var.client_proxy_repository_name
  image_tag       = "latest"
}

data "aws_ecr_image" "clickhouse_migrator" {
  repository_name = var.clickhouse_migrator_repository_name
  image_tag       = "latest"
}

// Its already set up in Nomad server config, but from there its taked only for newly created clusters so we need to make sure its apply here to existing.
resource "nomad_scheduler_config" "config" {
  memory_oversubscription_enabled = true
}

module "otel_collector" {
  source = "../../modules/job-otel-collector"

  provider_name = "aws"

  otel_collector_grpc_port = var.otel_collector_grpc_port

  grafana_otel_collector_token = var.grafana_otel_collector_token
  grafana_otlp_url             = var.grafana_otlp_url
  grafana_username             = var.grafana_username
  consul_token                 = var.consul_acl_token

  enable_otel_router_metrics = var.enable_otel_router_metrics
  otel_router_grpc_port      = var.otel_router_grpc_port

  clickhouse_username = var.clickhouse_username
  clickhouse_password = var.clickhouse_password
  clickhouse_port     = var.clickhouse_port
  clickhouse_database = var.clickhouse_database
}

module "otel_collector_nomad_server" {
  source = "../../modules/job-otel-collector-nomad-server"

  provider_name = "aws"
  node_pool     = var.api_node_pool

  grafana_otel_collector_token = var.grafana_otel_collector_token
  grafana_otlp_url             = var.grafana_otlp_url
  grafana_username             = var.grafana_username
}

module "redis" {
  source = "../../modules/job-redis"
  count  = var.redis_managed ? 0 : 1

  node_pool   = var.api_node_pool
  port_number = var.redis_port
  port_name   = "redis"
}

module "ingress" {
  source = "../../modules/job-ingress"

  ingress_count         = var.ingress_count
  ingress_port          = var.ingress_port
  ingress_internal_port = var.ingress_internal_port

  traefik_config_files = var.traefik_config_files

  node_pool     = var.api_node_pool
  update_stanza = var.api_cluster_size > 1

  nomad_token  = var.nomad_acl_token
  consul_token = var.consul_acl_token

  otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
}

module "client_proxy" {
  source = "../../modules/job-client-proxy"

  update_stanza      = var.api_cluster_size > 1
  client_proxy_count = var.client_proxy_count

  node_pool = var.api_node_pool

  image        = data.aws_ecr_image.client_proxy.image_uri
  job_env_vars = var.client_proxy_env_vars
}

module "api" {
  source = "../../modules/job-api"

  update_stanza      = var.api_cluster_size > 1
  node_pool          = var.api_node_pool
  prevent_colocation = var.api_cluster_size > 2
  count_instances    = var.api_cluster_size

  memory_mb = var.api_memory_mb
  cpu_count = var.api_cpu_count

  port_name                = "api"
  port_number              = var.api_port
  api_internal_grpc_port   = var.api_internal_grpc_port
  api_docker_image         = data.aws_ecr_image.api.image_uri
  db_migrator_docker_image = data.aws_ecr_image.db_migrator.image_uri
  job_env_vars             = var.api_env_vars
  db_migrator_env_vars     = var.api_db_migrator_env_vars
}

data "aws_s3_object" "orchestrator" {
  bucket = var.fc_env_pipeline_bucket_name
  key    = "orchestrator"
}

locals {
  orchestrator_artifact_source = "s3::https://${var.fc_env_pipeline_bucket_name}.s3.${var.aws_region}.amazonaws.com/orchestrator?etag=${data.aws_s3_object.orchestrator.etag}"
}

module "orchestrator" {
  source = "../../modules/job-orchestrator"

  node_pool  = var.orchestrator_node_pool
  port       = var.orchestrator_port
  proxy_port = var.orchestrator_proxy_port

  environment           = var.environment
  artifact_source       = local.orchestrator_artifact_source
  orchestrator_checksum = data.aws_s3_object.orchestrator.etag
  job_env_vars          = var.orchestrator_env_vars
}

data "aws_s3_object" "template_manager" {
  bucket = var.fc_env_pipeline_bucket_name
  key    = "template-manager"
}

locals {
  template_manager_artifact_source = "s3::https://${var.fc_env_pipeline_bucket_name}.s3.${var.aws_region}.amazonaws.com/template-manager?etag=${data.aws_s3_object.template_manager.etag}"
}

module "template_manager" {
  source = "../../modules/job-template-manager"

  update_stanza = var.build_cluster_size > 1
  node_pool     = var.build_node_pool

  port = var.template_manager_port

  artifact_source = local.template_manager_artifact_source
  job_env_vars    = var.template_manager_env_vars

  nomad_addr  = "https://nomad.${var.domain_name}"
  nomad_token = var.nomad_acl_token
}

data "aws_s3_object" "nomad_nodepool_apm" {
  count  = var.build_cluster_size > 1 ? 1 : 0
  bucket = var.fc_env_pipeline_bucket_name
  key    = "nomad-nodepool-apm"
}

module "template_manager_autoscaler" {
  source = "../../modules/job-template-manager-autoscaler"
  count  = var.build_cluster_size > 1 ? 1 : 0

  node_pool                  = var.api_node_pool
  nomad_token                = var.nomad_acl_token
  apm_plugin_artifact_source = "s3::https://${var.fc_env_pipeline_bucket_name}.s3.${var.aws_region}.amazonaws.com/nomad-nodepool-apm?etag=${data.aws_s3_object.nomad_nodepool_apm[0].etag}"
}

# ---
# Loki
# ---
module "loki" {
  source = "../../modules/job-loki"

  provider_name = "aws"
  aws_region    = var.aws_region

  node_pool          = var.api_node_pool
  prevent_colocation = var.api_cluster_size > 2
  bucket_name        = var.loki_bucket_name
  loki_port          = var.loki_port
}

# ---
# Logs Collector
# ---
module "logs_collector" {
  source = "../../modules/job-logs-collector"

  loki_endpoint = "http://loki.service.consul:${var.loki_port}"

  enable_otel_router_logs = var.enable_otel_router_logs
  otel_router_http_port   = var.otel_router_http_port

  vector_health_port = var.logs_health_proxy_port
  vector_api_port    = var.logs_proxy_port
}

# ---
# ClickHouse
# ---
module "clickhouse" {
  source = "../../modules/job-clickhouse"

  provider_name = "aws"

  node_pool             = var.clickhouse_node_pool
  job_constraint_prefix = var.clickhouse_jobs_prefix
  server_count          = var.clickhouse_cluster_size

  server_secret = var.clickhouse_server_secret

  cpu_count = var.clickhouse_cpu_count
  memory_mb = var.clickhouse_memory_mb

  clickhouse_database = var.clickhouse_database
  clickhouse_username = var.clickhouse_username
  clickhouse_password = var.clickhouse_password
  clickhouse_port     = var.clickhouse_port

  clickhouse_metrics_port = var.clickhouse_metrics_port
  otel_exporter_endpoint  = "http://localhost:${var.otel_collector_grpc_port}"

  aws_region    = var.aws_region
  backup_bucket = var.clickhouse_backups_bucket_name

  clickhouse_migrator_image = data.aws_ecr_image.clickhouse_migrator.image_uri
}
