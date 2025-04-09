terraform {
  required_version = ">= 1.5.0, < 1.6.0"
  backend "gcs" {
    prefix = "terraform/orchestration/state"
  }
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "3.0.2"
    }
    google = {
      source  = "hashicorp/google"
      version = "6.28.0"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "6.28.0"
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
    grafana = {
      source  = "grafana/grafana"
      version = "3.18.3"
    }
  }
}

data "google_client_config" "default" {}

provider "docker" {
  registry_auth {
    address  = "${var.gcp_region}-docker.pkg.dev"
    username = "oauth2accesstoken"
    password = data.google_client_config.default.access_token
  }
}

provider "google-beta" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}


module "init" {
  source = "./packages/init"

  labels = var.labels
  prefix = var.prefix
}

module "buckets" {
  source = "./packages/buckets"

  gcp_service_account_email = module.init.service_account_email
  gcp_project_id            = var.gcp_project_id
  gcp_region                = var.gcp_region

  fc_template_bucket_name     = length(var.template_bucket_name) > 0 ? var.template_bucket_name : "${var.gcp_project_id}-fc-templates"
  fc_template_bucket_location = var.template_bucket_location

  labels = var.labels
}

module "cluster" {
  source = "./packages/cluster"

  environment = var.environment

  cloudflare_api_token_secret_name = module.init.cloudflare_api_token_secret_name
  gcp_project_id                   = var.gcp_project_id
  gcp_region                       = var.gcp_region
  gcp_zone                         = var.gcp_zone
  google_service_account_key       = module.init.google_service_account_key

  server_cluster_size             = var.server_cluster_size
  client_cluster_size             = var.client_cluster_size
  client_cluster_auto_scaling_max = var.client_cluster_auto_scaling_max
  api_cluster_size                = var.api_cluster_size
  build_cluster_size              = var.build_cluster_size

  server_machine_type            = var.server_machine_type
  client_machine_type            = var.client_machine_type
  api_machine_type               = var.api_machine_type
  build_machine_type             = var.build_machine_type
  clickhouse_server_machine_type = var.clickhouse_server_machine_type
  clickhouse_keeper_machine_type = var.clickhouse_keeper_machine_type

  logs_health_proxy_port = var.logs_health_proxy_port
  logs_proxy_port        = var.logs_proxy_port

  client_proxy_health_port     = var.client_proxy_health_port
  client_proxy_port            = var.client_proxy_port
  api_port                     = var.api_port
  docker_reverse_proxy_port    = var.docker_reverse_proxy_port
  nomad_port                   = var.nomad_port
  google_service_account_email = module.init.service_account_email
  domain_name                  = var.domain_name
  additional_domains           = var.additional_domains != "" ? [for item in split(",", var.additional_domains) : trimspace(item)] : []

  docker_contexts_bucket_name = module.buckets.envs_docker_context_bucket_name
  cluster_setup_bucket_name   = module.buckets.cluster_setup_bucket_name
  fc_env_pipeline_bucket_name = module.buckets.fc_env_pipeline_bucket_name
  fc_kernels_bucket_name      = module.buckets.fc_kernels_bucket_name
  fc_versions_bucket_name     = module.buckets.fc_versions_bucket_name

  consul_acl_token_secret = module.init.consul_acl_token_secret
  nomad_acl_token_secret  = module.init.nomad_acl_token_secret

  notification_email_secret_version = module.init.notification_email_secret_version

  labels = var.labels
  prefix = var.prefix
}

module "api" {
  source = "./packages/api"

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  google_service_account_email  = module.init.service_account_email
  orchestration_repository_name = module.init.orchestration_repository_name

  labels = var.labels
  prefix = var.prefix
}

module "docker_reverse_proxy" {
  source = "./packages/docker-reverse-proxy"

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  custom_envs_repository_name   = module.api.custom_envs_repository_name
  orchestration_repository_name = module.init.orchestration_repository_name

  labels = var.labels
  prefix = var.prefix
}

module "client_proxy" {
  source = "./packages/client-proxy"

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  orchestration_repository_name = module.init.orchestration_repository_name
}

module "nomad" {
  source = "./packages/nomad"

  prefix              = var.prefix
  gcp_project_id      = var.gcp_project_id
  gcp_region          = var.gcp_region
  gcp_zone            = var.gcp_zone
  client_machine_type = var.client_machine_type

  consul_acl_token_secret = module.init.consul_acl_token_secret
  nomad_acl_token_secret  = module.init.nomad_acl_token_secret
  nomad_port              = var.nomad_port
  otel_tracing_print      = var.otel_tracing_print

  # Clickhouse
  clickhouse_connection_string = "clickhouse.service.consul:9000"
  clickhouse_username          = "clickhouse"
  clickhouse_password          = module.init.clickhouse_password_secret_data
  clickhouse_database          = "default"
  clickhouse_bucket_name       = module.buckets.clickhouse_bucket_name
  clickhouse_keeper_count      = module.cluster.monitoring_keeper_count
  clickhouse_server_count      = module.cluster.monitoring_server_count
  clickhouse_keeper_port       = module.cluster.monitoring_keeper_service_port
  clickhouse_server_port       = module.cluster.monitoring_server_service_port

  # API
  api_machine_count                         = var.api_cluster_size
  logs_proxy_address                        = "http://${module.cluster.logs_proxy_ip}"
  api_port                                  = var.api_port
  environment                               = var.environment
  google_service_account_key                = module.init.google_service_account_key
  api_docker_image_digest                   = module.api.api_docker_image_digest
  api_secret                                = module.api.api_secret
  custom_envs_repository_name               = module.api.custom_envs_repository_name
  postgres_connection_string_secret_name    = module.api.postgres_connection_string_secret_name
  supabase_jwt_secrets_secret_name          = module.api.supabase_jwt_secrets_secret_name
  posthog_api_key_secret_name               = module.api.posthog_api_key_secret_name
  analytics_collector_host_secret_name      = module.init.analytics_collector_host_secret_name
  analytics_collector_api_token_secret_name = module.init.analytics_collector_api_token_secret_name
  api_admin_token                           = module.api.api_admin_token
  redis_url_secret_version                  = module.api.redis_url_secret_version
  sandbox_access_token_hash_seed            = module.api.sandbox_access_token_hash_seed

  client_proxy_port                = var.client_proxy_port
  client_proxy_health_port         = var.client_proxy_health_port
  client_proxy_docker_image_digest = module.client_proxy.client_proxy_docker_image_digest

  domain_name = var.domain_name

  # Telemetry
  logs_health_proxy_port = var.logs_health_proxy_port
  logs_proxy_port        = var.logs_proxy_port

  # Logs
  loki_bucket_name  = module.buckets.loki_bucket_name
  loki_service_port = var.loki_service_port

  # Docker reverse proxy
  docker_reverse_proxy_docker_image_digest = module.docker_reverse_proxy.docker_reverse_proxy_docker_image_digest
  docker_reverse_proxy_port                = var.docker_reverse_proxy_port
  docker_reverse_proxy_service_account_key = module.docker_reverse_proxy.docker_reverse_proxy_service_account_key

  # Orchestrator
  orchestrator_port           = var.orchestrator_port
  orchestrator_proxy_port     = var.orchestrator_proxy_port
  fc_env_pipeline_bucket_name = module.buckets.fc_env_pipeline_bucket_name

  # Template manager
  template_manager_port          = var.template_manager_port
  template_bucket_name           = module.buckets.fc_template_bucket_name
  template_manager_machine_count = var.build_cluster_size

  # Redis
  redis_port = var.redis_port

  launch_darkly_api_key_secret_name = module.init.launch_darkly_api_key_secret_version.secret
}

module "redis" {
  source = "./terraform/redis"
  count  = var.redis_managed ? 1 : 0

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region
  gcp_zone       = var.gcp_zone

  prefix = var.prefix

  depends_on = [module.api]
}

module "grafana" {
  source          = "./terraform/grafana"
  grafana_managed = var.grafana_managed

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region
  prefix         = var.prefix
  domain_name    = var.domain_name
}
