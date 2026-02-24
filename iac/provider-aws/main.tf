terraform {
  required_version = ">= 1.5.0, < 1.6.0"

  backend "s3" {
    key = "terraform/orchestration/state"
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.33.0"
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

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = var.tags
  }
}

data "aws_secretsmanager_secret_version" "routing_domains" {
  secret_id = module.init.routing_domains_secret_name
}

locals {
  additional_domains = nonsensitive(jsondecode(data.aws_secretsmanager_secret_version.routing_domains.secret_string))

  template_manages_clusters_size_gt_1 = alltrue([for c in var.build_clusters_config : c.cluster_size > 1])
}

resource "random_password" "api_secret" {
  length  = 32
  special = false
}

resource "random_password" "api_admin_secret" {
  length  = 32
  special = false
}

resource "random_password" "sandbox_access_token_hash_seed" {
  length  = 32
  special = false
}

module "init" {
  source = "./init"

  tags          = var.tags
  prefix        = var.prefix
  bucket_prefix = var.bucket_prefix

  aws_region = var.aws_region

  template_bucket_name = var.template_bucket_name
}

module "network" {
  source = "./network"

  prefix             = var.prefix
  aws_region         = var.aws_region
  availability_zones = var.availability_zones
  vpc_cidr           = var.vpc_cidr

  tags = var.tags
}

module "efs" {
  source = "./efs"

  count = var.efs_cache_enabled ? 1 : 0

  prefix     = var.prefix
  vpc_id     = module.network.vpc_id
  subnet_ids = module.network.private_subnet_ids
  efs_sg_id  = module.network.efs_security_group_id

  tags = var.tags
}

module "database" {
  source = "./database"

  prefix     = var.prefix
  subnet_ids = module.network.private_subnet_ids
  rds_sg_id  = module.network.rds_security_group_id

  tags = var.tags
}

module "redis" {
  source = "./redis"
  count  = var.redis_managed ? 1 : 0

  prefix     = var.prefix
  subnet_ids = module.network.private_subnet_ids
  redis_sg_id = module.network.elasticache_security_group_id

  redis_node_type    = var.redis_node_type
  redis_shard_count  = var.redis_shard_count
  redis_replica_count = var.redis_replica_count

  redis_cluster_url_secret_arn = module.init.redis_cluster_url_secret_arn

  tags = var.tags
}

module "cluster" {
  source = "./nomad-cluster"

  environment = var.environment
  prefix      = var.prefix

  aws_region         = var.aws_region
  availability_zones = var.availability_zones

  vpc_id             = module.network.vpc_id
  public_subnet_ids  = module.network.public_subnet_ids
  private_subnet_ids = module.network.private_subnet_ids
  cluster_sg_id      = module.network.nomad_cluster_security_group_id

  iam_instance_profile_name = module.init.iam_instance_profile_name

  build_clusters_config  = var.build_clusters_config
  client_clusters_config = var.client_clusters_config

  server_cluster_size    = var.server_cluster_size
  server_instance_type   = var.server_instance_type
  api_cluster_size       = var.api_cluster_size
  api_instance_type      = var.api_instance_type

  ami_id = var.ami_id

  nomad_acl_token_secret  = module.init.nomad_acl_token_secret
  consul_acl_token_secret = module.init.consul_acl_token_secret

  cluster_setup_bucket_name   = module.init.cluster_setup_bucket_name
  fc_kernels_bucket_name      = module.init.fc_kernels_bucket_name
  fc_versions_bucket_name     = module.init.fc_versions_bucket_name
  fc_env_pipeline_bucket_name = module.init.fc_env_pipeline_bucket_name
  docker_contexts_bucket_name = module.init.envs_docker_context_bucket_name

  nomad_port = var.nomad_port
  domain_name = var.domain_name

  api_port                 = var.api_port
  ingress_port             = var.ingress_port
  docker_reverse_proxy_port = var.docker_reverse_proxy_port
  client_proxy_port        = var.client_proxy_port
  client_proxy_health_port = var.client_proxy_health_port

  efs_cache_enabled = var.efs_cache_enabled
  efs_dns_name      = var.efs_cache_enabled ? module.efs[0].efs_dns_name : ""
  efs_mount_path    = "/orchestrator/shared-store"

  tags = var.tags
}

module "load_balancer" {
  source = "./load-balancer"

  prefix = var.prefix

  vpc_id            = module.network.vpc_id
  public_subnet_ids = module.network.public_subnet_ids
  alb_sg_id         = module.network.alb_security_group_id

  domain_name        = var.domain_name
  additional_domains = local.additional_domains

  api_port                  = var.api_port
  ingress_port              = var.ingress_port
  docker_reverse_proxy_port = var.docker_reverse_proxy_port
  nomad_port                = var.nomad_port
  client_proxy_port         = var.client_proxy_port

  api_asg_id    = module.cluster.api_asg_id
  server_asg_id = module.cluster.server_asg_id

  cloudflare_api_token_secret_arn = module.init.cloudflare_api_token_secret_arn

  tags = var.tags
}

module "nomad" {
  source = "./nomad"

  prefix      = var.prefix
  aws_region  = var.aws_region
  environment = var.environment

  consul_acl_token_secret = module.init.consul_acl_token_secret
  nomad_acl_token_secret  = module.init.nomad_acl_token_secret
  nomad_port              = var.nomad_port
  domain_name             = var.domain_name

  core_repository_url = module.init.core_repository_url

  # API
  api_port                  = var.api_port
  api_resources_cpu_count   = var.api_resources_cpu_count
  api_resources_memory_mb   = var.api_resources_memory_mb
  api_machine_count         = var.api_cluster_size
  api_node_pool             = var.api_node_pool
  api_secret                = random_password.api_secret.result
  api_admin_token           = random_password.api_admin_secret.result
  sandbox_access_token_hash_seed = random_password.sandbox_access_token_hash_seed.result

  postgres_connection_string_secret_arn                 = module.init.postgres_connection_string_secret_arn
  postgres_read_replica_connection_string_secret_arn    = module.init.postgres_read_replica_connection_string_secret_arn
  supabase_jwt_secrets_secret_arn                       = module.init.supabase_jwt_secrets_secret_arn
  posthog_api_key_secret_arn                            = module.init.posthog_api_key_secret_arn
  analytics_collector_host_secret_arn                   = module.init.analytics_collector_host_secret_arn
  analytics_collector_api_token_secret_arn              = module.init.analytics_collector_api_token_secret_arn
  launch_darkly_api_key_secret_arn                      = module.init.launch_darkly_api_key_secret_arn
  redis_cluster_url_secret_arn                          = module.init.redis_cluster_url_secret_arn
  redis_tls_ca_base64_secret_arn                        = module.init.redis_tls_ca_base64_secret_arn

  # Ingress
  ingress_port  = var.ingress_port
  ingress_count = var.ingress_count

  # Client Proxy
  client_proxy_count               = var.client_proxy_count
  client_proxy_resources_cpu_count = var.client_proxy_resources_cpu_count
  client_proxy_resources_memory_mb = var.client_proxy_resources_memory_mb
  client_proxy_update_max_parallel = var.client_proxy_update_max_parallel
  client_proxy_session_port        = var.client_proxy_port.port
  client_proxy_health_port         = var.client_proxy_health_port.port

  # Docker reverse proxy
  docker_reverse_proxy_port = var.docker_reverse_proxy_port

  # Orchestrator
  orchestrator_node_pool      = var.orchestrator_node_pool
  orchestrator_port           = var.orchestrator_port
  orchestrator_proxy_port     = var.orchestrator_proxy_port
  fc_env_pipeline_bucket_name = module.init.fc_env_pipeline_bucket_name
  allow_sandbox_internet      = var.allow_sandbox_internet
  envd_timeout                = var.envd_timeout

  # Template manager
  builder_node_pool                   = var.build_node_pool
  template_manager_port               = var.template_manager_port
  template_bucket_name                = module.init.fc_template_bucket_name
  build_cache_bucket_name             = module.init.fc_build_cache_bucket_name
  template_manages_clusters_size_gt_1 = local.template_manages_clusters_size_gt_1

  # Logs
  loki_node_pool           = var.loki_node_pool
  loki_machine_count       = var.loki_cluster_size
  loki_resources_memory_mb = var.loki_resources_memory_mb
  loki_resources_cpu_count = var.loki_resources_cpu_count
  loki_bucket_name         = module.init.loki_bucket_name
  loki_service_port        = var.loki_service_port

  # Otel Collector
  otel_collector_resources_memory_mb = var.otel_collector_resources_memory_mb
  otel_collector_resources_cpu_count = var.otel_collector_resources_cpu_count

  # Clickhouse
  clickhouse_resources_cpu_count   = var.clickhouse_resources_cpu_count
  clickhouse_resources_memory_mb   = var.clickhouse_resources_memory_mb
  clickhouse_database              = var.clickhouse_database_name
  clickhouse_backups_bucket_name   = module.init.clickhouse_backups_bucket_name
  clickhouse_server_count          = var.clickhouse_cluster_size
  clickhouse_server_port           = var.clickhouse_server_service_port
  clickhouse_job_constraint_prefix = var.clickhouse_job_constraint_prefix
  clickhouse_node_pool             = var.clickhouse_node_pool

  # Redis
  redis_managed = var.redis_managed
  redis_port    = var.redis_port

  # Filestore / EFS
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
