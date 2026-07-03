terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.35.1"
    }

    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.48.0"
    }

    nomad = {
      source  = "hashicorp/nomad"
      version = "2.1.0"
    }

    random = {
      source  = "hashicorp/random"
      version = "~> 3.1"
    }
  }

  required_version = ">= 1.0"

  backend "s3" {
    key = "terraform/orchestration/state"
  }
}

provider "cloudflare" {
  api_token = module.init.cloudflare.token
}

provider "aws" {}

provider "nomad" {
  address      = "https://nomad.${var.domain_name}"
  secret_id    = module.init.cluster.nomad_acl_token
  consul_token = module.init.cluster.consul_acl_token
}

data "aws_region" "current" {}

data "aws_caller_identity" "current" {}

data "aws_elb_service_account" "current" {}

module "init" {
  source = "./init"

  prefix        = var.prefix
  bucket_prefix = var.bucket_prefix

  region = data.aws_region.current.id
  endpoint_ingress_subnet_ids = [
    aws_security_group.cluster_node.id
  ]

  allow_force_destroy = var.allow_force_destroy
}

resource "random_password" "volume_token_key" {
  length  = 32
  special = false

  lifecycle {
    ignore_changes = [length, special]
  }
}

locals {
  redis_port            = 6379
  ingress_port          = 8080
  ingress_internal_port = 9435
  nomad_port            = 4646
  clickhouse_port       = 9000
  clickhouse_database   = "default"
  loki_port             = 3100
  logs_proxy_port       = 30006
  otel_collector_port   = 4317

  auth_provider_config = {
    jwt = []
  }

  api_pool_name          = "api"
  client_pool_name       = "default"
  build_pool_name        = "build"
  clickhouse_pool_name   = "clickhouse"
  clickhouse_jobs_prefix = "clickhouse"

  # AMI name prefix must match what Packer produces: "${var.prefix}orch-<timestamp>"
  ami_family_prefix = "${var.prefix}orch-"

  redis_cluster_url   = var.redis_managed ? "rediss://${module.redis[0].endpoint_address}:${local.redis_port}" : ""
  redis_tls_ca_base64 = var.redis_managed ? module.redis[0].endpoint_ca_pem_base64 : ""
  redis_url           = local.redis_cluster_url == "" ? "redis.service.consul:${local.redis_port}" : ""

  clickhouse_connection_string = var.clickhouse_cluster_size > 0 ? "clickhouse://${module.init.clickhouse.username}:${module.init.clickhouse.password}@clickhouse.service.consul:${local.clickhouse_port}/${local.clickhouse_database}" : ""

  # The Nomad jobspec template renders each entry as `${key} = "${value}"`,
  # so values that themselves contain `"` characters (like a JSON blob)
  # must have those quotes pre-escaped to produce valid HCL.
  api_env_vars = merge({
    ENVIRONMENT                    = var.environment
    GIN_MODE                       = "release"
    DOMAIN_NAME                    = var.domain_name
    NOMAD_TOKEN                    = module.init.cluster.nomad_acl_token
    ORCHESTRATOR_PORT              = tostring(var.orchestrator_port)
    API_INTERNAL_GRPC_PORT         = tostring(var.api_internal_grpc_port)
    ADMIN_TOKEN                    = module.init.admin_token
    SANDBOX_ACCESS_TOKEN_HASH_SEED = module.init.sandbox_access_token_hash_seed
    AUTH_PROVIDER_CONFIG           = replace(jsonencode(local.auth_provider_config), "\"", "\\\"")

    POSTGRES_CONNECTION_STRING   = module.init.postgres_connection_string
    DB_MAX_OPEN_CONNECTIONS      = tostring(var.db_max_open_connections)
    DB_MIN_IDLE_CONNECTIONS      = tostring(var.db_min_idle_connections)
    AUTH_DB_CONNECTION_STRING    = module.init.postgres_connection_string
    AUTH_DB_MAX_OPEN_CONNECTIONS = tostring(var.auth_db_max_open_connections)
    AUTH_DB_MIN_IDLE_CONNECTIONS = tostring(var.auth_db_min_idle_connections)

    LOKI_URL                     = "http://loki.service.consul:${local.loki_port}"
    CLICKHOUSE_CONNECTION_STRING = local.clickhouse_connection_string

    LOGS_COLLECTOR_ADDRESS       = "http://localhost:${local.logs_proxy_port}"
    OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:${local.otel_collector_port}"

    REDIS_POOL_SIZE     = "160"
    REDIS_CLUSTER_URL   = local.redis_cluster_url
    REDIS_TLS_CA_BASE64 = local.redis_tls_ca_base64
    REDIS_URL           = local.redis_url

    LAUNCH_DARKLY_API_KEY = module.init.launch_darkly_api_key
    # This is here just because it is required in some part of our code which is transitively imported
    TEMPLATE_BUCKET_NAME = "skip"

    VOLUME_TOKEN_ISSUER           = var.domain_name
    VOLUME_TOKEN_SIGNING_KEY      = "HMAC:${base64encode(random_password.volume_token_key.result)}"
    VOLUME_TOKEN_SIGNING_KEY_NAME = "e2b-volume-token-key"
    VOLUME_TOKEN_DURATION         = "1h"
    VOLUME_TOKEN_SIGNING_METHOD   = "HS256"
  }, var.api_env_vars)

  api_db_migrator_env_vars = merge({
    POSTGRES_CONNECTION_STRING = module.init.postgres_connection_string
  }, var.api_db_migrator_env_vars)

  client_proxy_env_vars = merge({
    ENVIRONMENT                  = var.environment
    OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:${local.otel_collector_port}"
    LOGS_COLLECTOR_ADDRESS       = "http://localhost:${local.logs_proxy_port}"
    REDIS_POOL_SIZE              = "40"
    REDIS_CLUSTER_URL            = local.redis_cluster_url
    REDIS_TLS_CA_BASE64          = local.redis_tls_ca_base64
    REDIS_URL                    = local.redis_url
    # Used by in-cluster client-proxy to call API ResumeSandbox over gRPC.
    API_INTERNAL_GRPC_ADDRESS = "api-internal-grpc.service.consul:${var.api_internal_grpc_port}"
    LAUNCH_DARKLY_API_KEY     = module.init.launch_darkly_api_key
  }, var.client_proxy_env_vars)

  orchestrator_env_vars = merge({
    LOGS_COLLECTOR_ADDRESS       = "http://localhost:${local.logs_proxy_port}"
    ENVIRONMENT                  = var.environment
    ENVD_TIMEOUT                 = var.envd_timeout
    TEMPLATE_BUCKET_NAME         = module.init.fc_template_bucket_name
    OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:${local.otel_collector_port}"
    ALLOW_SANDBOX_INTERNAL_CIDRS = var.allow_sandbox_internal_cidrs
    CLICKHOUSE_CONNECTION_STRING = local.clickhouse_connection_string
    REDIS_POOL_SIZE              = "10"
    REDIS_CLUSTER_URL            = local.redis_cluster_url
    REDIS_TLS_CA_BASE64          = local.redis_tls_ca_base64
    REDIS_URL                    = local.redis_url
    GIN_MODE                     = "release"
    CONSUL_TOKEN                 = module.init.cluster.consul_acl_token
    DOMAIN_NAME                  = var.domain_name
    SHARED_CHUNK_CACHE_PATH      = ""
    ORCHESTRATOR_SERVICES        = "orchestrator"
    PROVIDER                     = "aws"
    BUILD_CACHE_BUCKET_NAME      = module.init.fc_template_build_cache_bucket_name
    LAUNCH_DARKLY_API_KEY        = module.init.launch_darkly_api_key
    ARTIFACTS_REGISTRY_PROVIDER  = "AWS_ECR"
    STORAGE_PROVIDER             = "AWSBucket"
    S3_USE_PATH_STYLE            = tostring(var.s3_use_path_style)
    AWS_REGION                   = data.aws_region.current.id
    AWS_DOCKER_REPOSITORY_NAME   = module.init.custom_environments_repository_name
  }, var.orchestrator_env_vars)

  template_manager_env_vars = merge({
    CONSUL_TOKEN                 = module.init.cluster.consul_acl_token
    ARTIFACTS_REGISTRY_PROVIDER  = "AWS_ECR"
    STORAGE_PROVIDER             = "AWSBucket"
    S3_USE_PATH_STYLE            = tostring(var.s3_use_path_style)
    AWS_REGION                   = data.aws_region.current.id
    AWS_DOCKER_REPOSITORY_NAME   = module.init.custom_environments_repository_name
    API_SECRET                   = module.init.api_secret
    ENVIRONMENT                  = var.environment
    DOMAIN_NAME                  = var.domain_name
    TEMPLATE_BUCKET_NAME         = module.init.fc_template_bucket_name
    BUILD_CACHE_BUCKET_NAME      = module.init.fc_template_build_cache_bucket_name
    OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:${local.otel_collector_port}"
    LOGS_COLLECTOR_ADDRESS       = "http://localhost:${local.logs_proxy_port}"
    ORCHESTRATOR_SERVICES        = "template-manager"
    REDIS_POOL_SIZE              = "10"
    CLICKHOUSE_CONNECTION_STRING = local.clickhouse_connection_string
    GIN_MODE                     = "release"
    LAUNCH_DARKLY_API_KEY        = module.init.launch_darkly_api_key
  }, var.template_manager_env_vars)
}

module "redis" {
  source = "./modules/redis"
  count  = var.redis_managed ? 1 : 0

  prefix            = var.prefix
  vpc_id            = module.init.vpc_id
  subnet_group_name = module.init.vpc_elasticache_subnet_group_name

  port          = local.redis_port
  instance_type = var.redis_instance_type
  replica_size  = var.redis_replica_size
  ingress_security_group_ids = [
    aws_security_group.cluster_node.id
  ]
}

module "cluster" {
  source = "./nomad-cluster"

  prefix         = var.prefix
  aws_account_id = data.aws_caller_identity.current.account_id
  aws_region     = data.aws_region.current.id

  nomad_acl_token_secret          = module.init.cluster.nomad_acl_token
  consul_acl_token_secret         = module.init.cluster.consul_acl_token
  consul_dns_request_token_secret = module.init.cluster.consul_dns_request_token
  consul_gossip_encryption_key    = module.init.cluster.consul_gossip_encryption_key

  control_server_cluster_size        = var.control_server_cluster_size
  control_server_image_family_prefix = var.control_server_image_family_prefix != "" ? var.control_server_image_family_prefix : local.ami_family_prefix
  control_server_machine_type        = var.control_server_machine_type
  control_server_target_group_arns   = [aws_lb_target_group.nomad.arn]
  control_server_security_group_ids  = [aws_security_group.cluster_node.id]

  vpc_private_subnets = module.init.vpc_private_subnet_ids
  vpc_public_subnets  = module.init.vpc_public_subnet_ids

  custom_environments_repository_name = module.init.custom_environments_repository_name

  setup_bucket_name                 = module.init.setup_bucket_name
  fc_env_pipeline_bucket_name       = module.init.fc_env_pipeline_bucket_name
  fc_kernels_bucket_name            = module.init.fc_kernels_bucket_name
  fc_versions_bucket_name           = module.init.fc_versions_bucket_name
  fc_busybox_bucket_name            = module.init.fc_busybox_bucket_name
  templates_bucket_name             = module.init.fc_template_bucket_name
  templates_build_cache_bucket_name = module.init.fc_template_build_cache_bucket_name
  loki_bucket_name                  = module.init.loki_bucket_name
  clickhouse_backups_bucket_name    = module.init.clickhouse_backups_bucket_name

  api_node_pool_name      = local.api_pool_name
  api_cluster_size        = var.api_cluster_size
  api_image_family_prefix = var.api_image_family_prefix != "" ? var.api_image_family_prefix : local.ami_family_prefix
  api_machine_type        = var.api_server_machine_type
  api_security_group_ids  = [aws_security_group.cluster_node.id]
  api_target_group_arns = [
    aws_lb_target_group.ingress.arn,
    aws_lb_target_group.ingress_grpc.arn,
  ]

  build_node_pool_name               = local.build_pool_name
  build_cluster_size                 = var.build_cluster_size
  build_image_family_prefix          = local.ami_family_prefix
  build_machine_type                 = var.build_server_machine_type
  build_server_nested_virtualization = var.build_server_nested_virtualization
  build_security_group_ids           = [aws_security_group.cluster_node.id]
  build_node_labels                  = var.build_node_labels

  client_node_pool_name               = local.client_pool_name
  client_cluster_size                 = var.client_cluster_size
  client_image_family_prefix          = var.client_image_family_prefix != "" ? var.client_image_family_prefix : local.ami_family_prefix
  client_machine_type                 = var.client_server_machine_type
  client_security_group_ids           = [aws_security_group.cluster_node.id]
  client_server_nested_virtualization = var.client_server_nested_virtualization
  client_node_labels                  = var.client_node_labels

  clickhouse_az                    = "${data.aws_region.current.id}a"
  clickhouse_cluster_size          = var.clickhouse_cluster_size
  clickhouse_image_family_prefix   = var.clickhouse_image_family_prefix != "" ? var.clickhouse_image_family_prefix : local.ami_family_prefix
  clickhouse_machine_type          = var.clickhouse_server_machine_type
  clickhouse_node_pool_name        = local.clickhouse_pool_name
  clickhouse_security_group_ids    = [aws_security_group.cluster_node.id]
  clickhouse_subnet_id             = module.init.vpc_private_subnet_ids[0]
  clickhouse_job_constraint_prefix = local.clickhouse_jobs_prefix
}

module "nomad" {
  source = "./nomad"

  domain_name = var.domain_name
  environment = var.environment
  aws_region  = data.aws_region.current.id

  nomad_acl_token  = module.init.cluster.nomad_acl_token
  consul_acl_token = module.init.cluster.consul_acl_token

  grafana_otel_collector_token = module.init.grafana.otel_collector_token
  grafana_otlp_url             = module.init.grafana.otlp_url
  grafana_username             = module.init.grafana.username

  api_node_pool          = local.api_pool_name
  clickhouse_node_pool   = local.clickhouse_pool_name
  clickhouse_jobs_prefix = local.clickhouse_jobs_prefix

  api_cluster_size            = var.api_cluster_size
  api_internal_grpc_port      = var.api_internal_grpc_port
  api_env_vars                = local.api_env_vars
  api_db_migrator_env_vars    = local.api_db_migrator_env_vars
  api_repository_name         = module.init.api_repository_name
  db_migrator_repository_name = module.init.db_migrator_repository_name

  ingress_count         = var.ingress_count
  ingress_port          = local.ingress_port
  ingress_internal_port = local.ingress_internal_port

  traefik_config_files = var.traefik_config_files

  client_proxy_count           = var.client_proxy_count
  client_proxy_repository_name = module.init.client_proxy_repository_name
  client_proxy_env_vars        = local.client_proxy_env_vars

  orchestrator_node_pool              = local.client_pool_name
  orchestrator_port                   = var.orchestrator_port
  orchestrator_proxy_port             = var.orchestrator_proxy_port
  fc_env_pipeline_bucket_name         = module.init.fc_env_pipeline_bucket_name
  template_bucket_name                = module.init.fc_template_bucket_name
  build_cache_bucket_name             = module.init.fc_template_build_cache_bucket_name
  custom_environments_repository_name = module.init.custom_environments_repository_name
  orchestrator_env_vars               = local.orchestrator_env_vars
  template_manager_env_vars           = local.template_manager_env_vars

  build_node_pool    = local.build_pool_name
  build_cluster_size = var.build_cluster_size
  redis_managed      = var.redis_managed
  redis_port         = local.redis_port

  loki_bucket_name = module.init.loki_bucket_name
  loki_port        = local.loki_port

  clickhouse_cluster_size             = var.clickhouse_cluster_size
  clickhouse_username                 = module.init.clickhouse.username
  clickhouse_password                 = module.init.clickhouse.password
  clickhouse_server_secret            = module.init.clickhouse.server_secret
  clickhouse_port                     = local.clickhouse_port
  clickhouse_database                 = local.clickhouse_database
  clickhouse_backups_bucket_name      = module.init.clickhouse_backups_bucket_name
  clickhouse_migrator_repository_name = module.init.clickhouse_migrator_repository_name

  launch_darkly_api_key = module.init.launch_darkly_api_key

  otel_collector_grpc_port = local.otel_collector_port
  logs_proxy_port          = local.logs_proxy_port

  enable_otel_router_logs = var.enable_otel_router_logs
  otel_router_http_port   = var.otel_router_http_port

  enable_otel_router_metrics = var.enable_otel_router_metrics
  otel_router_grpc_port      = var.otel_router_grpc_port

}

resource "aws_security_group" "cluster_node" {
  name        = "${var.prefix}cluster-node"
  description = "Basic security group for cluster nodes"
  vpc_id      = module.init.vpc_id

  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "TCP"
    description = "AWS EC2 Instance Connect"
    security_groups = [
      module.init.vpc_instance_connect_security_group_id
    ]
  }

  ingress {
    from_port   = local.nomad_port
    to_port     = local.nomad_port
    protocol    = "TCP"
    description = "Nomad API/UI communication from load balancer"
    security_groups = [
      aws_security_group.ingress.id
    ]
  }

  ingress {
    from_port   = local.ingress_port
    to_port     = local.ingress_port
    protocol    = "TCP"
    description = "Ingress communication from load balancer"
    security_groups = [
      aws_security_group.ingress.id
    ]
  }

  ingress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    description = "Allow communication between cluster nodes"
    self        = true
  }

  egress {
    from_port        = 0
    to_port          = 0
    protocol         = "-1"
    description      = "Allow all outbound traffic"
    cidr_blocks      = ["0.0.0.0/0"]
    ipv6_cidr_blocks = ["::/0"]
  }

  tags = {
    Name = "${var.prefix}cluster-node"
  }
}
