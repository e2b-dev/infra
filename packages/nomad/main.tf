terraform {
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "3.0.2"
    }
  }
}

# API
data "google_secret_manager_secret_version" "postgres_connection_string" {
  secret = var.postgres_connection_string_secret_name
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

data "google_secret_manager_secret_version" "redis_url" {
  secret = var.redis_url_secret_version.secret
}


data "docker_registry_image" "api_image" {
  name = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${var.orchestration_repository_name}/api:latest"
}

resource "docker_image" "api_image" {
  name          = data.docker_registry_image.api_image.name
  pull_triggers = [data.docker_registry_image.api_image.sha256_digest]
  platform      = "linux/amd64/v8"
}

data "docker_registry_image" "db_migrator_image" {
  name = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${var.orchestration_repository_name}/db-migrator:latest"
}

resource "docker_image" "db_migrator_image" {
  name          = data.docker_registry_image.db_migrator_image.name
  pull_triggers = [data.docker_registry_image.db_migrator_image.sha256_digest]
  platform      = "linux/amd64/v8"
}

resource "nomad_job" "api" {
  jobspec = templatefile("${path.module}/api.hcl", {
    update_stanza = var.api_machine_count > 1
    // We use colocation 2 here to ensure that there are at least 2 nodes for API to do rolling updates.
    // It might be possible there could be problems if we are rolling updates for both API and Loki at the same time., so maybe increasing this to > 3 makes sense.
    prevent_colocation             = var.api_machine_count > 2
    orchestrator_port              = var.orchestrator_port
    template_manager_host          = "template-manager.service.consul:${var.template_manager_port}"
    otel_collector_grpc_endpoint   = "localhost:${var.otel_collector_grpc_port}"
    loki_address                   = "http://loki.service.consul:${var.loki_service_port.port}"
    logs_collector_address         = "http://localhost:${var.logs_proxy_port.port}"
    gcp_zone                       = var.gcp_zone
    port_name                      = var.api_port.name
    port_number                    = var.api_port.port
    api_docker_image               = docker_image.api_image.repo_digest
    postgres_connection_string     = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
    supabase_jwt_secrets           = data.google_secret_manager_secret_version.supabase_jwt_secrets.secret_data
    posthog_api_key                = data.google_secret_manager_secret_version.posthog_api_key.secret_data
    environment                    = var.environment
    analytics_collector_host       = data.google_secret_manager_secret_version.analytics_collector_host.secret_data
    analytics_collector_api_token  = data.google_secret_manager_secret_version.analytics_collector_api_token.secret_data
    otel_tracing_print             = var.otel_tracing_print
    nomad_acl_token                = var.nomad_acl_token_secret
    admin_token                    = var.api_admin_token
    redis_url                      = data.google_secret_manager_secret_version.redis_url.secret_data != "redis.service.consul" ? "${data.google_secret_manager_secret_version.redis_url.secret_data}:${var.redis_port.port}" : "redis.service.consul:${var.redis_port.port}"
    dns_port_number                = var.api_dns_port_number
    clickhouse_connection_string   = "clickhouse.service.consul:9000"
    clickhouse_username            = var.clickhouse_username
    clickhouse_password            = random_password.clickhouse_password.result
    clickhouse_database            = var.clickhouse_database
    sandbox_access_token_hash_seed = var.sandbox_access_token_hash_seed
    db_migrator_docker_image       = docker_image.db_migrator_image.repo_digest
  })
}

resource "nomad_job" "redis" {
  # Uncomment after the migration period
  # count = data.google_secret_manager_secret_version.redis_url.secret_data == "redis.service.consul" ? 1 : 0

  jobspec = templatefile("${path.module}/redis.hcl",
    {
      gcp_zone    = var.gcp_zone
      port_number = var.redis_port.port
      port_name   = var.redis_port.name
    }
  )
}

data "docker_registry_image" "docker_reverse_proxy_image" {
  name = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${var.orchestration_repository_name}/docker-reverse-proxy"
}

resource "docker_image" "docker_reverse_proxy_image" {
  name          = data.docker_registry_image.docker_reverse_proxy_image.name
  pull_triggers = [data.docker_registry_image.docker_reverse_proxy_image.sha256_digest]
  platform      = "linux/amd64/v8"
}


resource "nomad_job" "docker_reverse_proxy" {
  jobspec = file("${path.module}/docker-reverse-proxy.hcl")

  hcl2 {
    vars = {
      gcp_zone                      = var.gcp_zone
      image_name                    = docker_image.docker_reverse_proxy_image.repo_digest
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
  }
}

data "docker_registry_image" "proxy_image" {
  name = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${var.orchestration_repository_name}/client-proxy"
}

resource "docker_image" "client_proxy_image" {
  name          = data.docker_registry_image.proxy_image.name
  pull_triggers = [data.docker_registry_image.proxy_image.sha256_digest]
  platform      = "linux/amd64/v8"
}

resource "nomad_job" "client_proxy" {
  jobspec = templatefile("${path.module}/edge.hcl",
    {
      update_stanza = var.api_machine_count > 1
      count         = var.client_proxy_count
      cpu_count     = var.client_proxy_resources_cpu_count
      memory_mb     = var.client_proxy_resources_memory_mb

      gcp_zone    = var.gcp_zone
      environment = var.environment

      redis_url = data.google_secret_manager_secret_version.redis_url.secret_data != "redis.service.consul" ? "${data.google_secret_manager_secret_version.redis_url.secret_data}:${var.redis_port.port}" : "redis.service.consul:${var.redis_port.port}"
      loki_url  = "http://loki.service.consul:${var.loki_service_port.port}"

      proxy_port_name   = var.edge_proxy_port.name
      proxy_port        = var.edge_proxy_port.port
      api_port_name     = var.edge_api_port.name
      api_port          = var.edge_api_port.port
      api_secret        = var.edge_api_secret
      orchestrator_port = var.orchestrator_port

      environment = var.environment
      image_name  = docker_image.client_proxy_image.repo_digest

      otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
      logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
      launch_darkly_api_key        = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  })
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

resource "nomad_job" "otel_collector" {
  jobspec = templatefile("${path.module}/otel-collector.hcl", {
    memory_mb = var.otel_collector_resources_memory_mb
    cpu_count = var.otel_collector_resources_cpu_count
    gcp_zone  = var.gcp_zone

    otel_collector_grpc_port = var.otel_collector_grpc_port

    otel_collector_config = templatefile("${path.module}/configs/otel-collector.yaml", {
      grafana_otel_collector_token = data.google_secret_manager_secret_version.grafana_otel_collector_token.secret_data
      grafana_otlp_url             = data.google_secret_manager_secret_version.grafana_otlp_url.secret_data
      grafana_username             = data.google_secret_manager_secret_version.grafana_username.secret_data
      consul_token                 = var.consul_acl_token_secret

      clickhouse_username = var.clickhouse_username
      clickhouse_password = random_password.clickhouse_password.result
      clickhouse_port     = var.clickhouse_server_port.port
      clickhouse_host     = "clickhouse.service.consul"
      clickhouse_database = var.clickhouse_database
    })
  })
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


resource "nomad_job" "logs_collector" {
  jobspec = templatefile("${path.module}/logs-collector.hcl", {
    gcp_zone = var.gcp_zone

    logs_port_number        = var.logs_proxy_port.port
    logs_health_port_number = var.logs_health_proxy_port.port
    logs_health_path        = var.logs_health_proxy_port.health_path
    logs_port_name          = var.logs_proxy_port.name

    loki_service_port_number = var.loki_service_port.port

    grafana_logs_user     = data.google_secret_manager_secret_version.grafana_logs_user.secret_data
    grafana_logs_endpoint = data.google_secret_manager_secret_version.grafana_logs_url.secret_data
    grafana_api_key       = data.google_secret_manager_secret_version.grafana_logs_collector_api_token.secret_data
  })
}

data "google_storage_bucket_object" "orchestrator" {
  name   = "orchestrator"
  bucket = var.fc_env_pipeline_bucket_name
}

data "external" "orchestrator_checksum" {
  program = ["bash", "${path.module}/checksum.sh"]

  query = {
    base64 = data.google_storage_bucket_object.orchestrator.md5hash
  }
}


locals {
  orchestrator_envs = {
    port             = var.orchestrator_port
    proxy_port       = var.orchestrator_proxy_port
    environment      = var.environment
    consul_acl_token = var.consul_acl_token_secret

    bucket_name                  = var.fc_env_pipeline_bucket_name
    orchestrator_checksum        = data.external.orchestrator_checksum.result.hex
    logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
    logs_collector_public_ip     = var.logs_proxy_address
    otel_tracing_print           = var.otel_tracing_print
    template_bucket_name         = var.template_bucket_name
    template_cache_proxy_url     = var.template_cache_proxy_url
    otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
    allow_sandbox_internet       = var.allow_sandbox_internet
    launch_darkly_api_key        = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  }

  orchestrator_job_check = templatefile("${path.module}/orchestrator.hcl", merge(
    local.orchestrator_envs,
    {
      latest_orchestrator_job_id = "placeholder",
    }
  ))
}



resource "random_id" "orchestrator_job" {
  keepers = {
    # Use both the orchestrator job (including vars) definition and the latest orchestrator checksum to detect changes
    orchestrator_job = sha256("${local.orchestrator_job_check}-${data.external.orchestrator_checksum.result.hex}")
  }

  byte_length = 8
}

locals {
  latest_orchestrator_job_id = var.environment == "dev" ? "dev" : random_id.orchestrator_job.hex
}

resource "nomad_variable" "orchestrator_hash" {
  path = "nomad/jobs"
  items = {
    latest_orchestrator_job_id = local.latest_orchestrator_job_id
  }
}

resource "nomad_job" "orchestrator" {
  deregister_on_id_change = false

  jobspec = templatefile("${path.module}/orchestrator.hcl", merge(
    local.orchestrator_envs,
    {
      latest_orchestrator_job_id = local.latest_orchestrator_job_id
    }
  ))

  depends_on = [nomad_variable.orchestrator_hash, random_id.orchestrator_job]
}

data "google_storage_bucket_object" "template_manager" {
  name   = "template-manager"
  bucket = var.fc_env_pipeline_bucket_name
}


data "external" "template_manager" {
  program = ["bash", "${path.module}/checksum.sh"]

  query = {
    base64 = data.google_storage_bucket_object.template_manager.md5hash
  }
}

resource "nomad_job" "template_manager" {
  jobspec = templatefile("${path.module}/template-manager.hcl", {
    update_stanza = var.template_manager_machine_count > 1

    gcp_project      = var.gcp_project_id
    gcp_region       = var.gcp_region
    gcp_zone         = var.gcp_zone
    port             = var.template_manager_port
    environment      = var.environment
    consul_acl_token = var.consul_acl_token_secret

    api_secret                   = var.api_secret
    bucket_name                  = var.fc_env_pipeline_bucket_name
    docker_registry              = var.custom_envs_repository_name
    google_service_account_key   = var.google_service_account_key
    template_manager_checksum    = data.external.template_manager.result.hex
    otel_tracing_print           = var.otel_tracing_print
    template_bucket_name         = var.template_bucket_name
    template_cache_proxy_url     = var.template_cache_proxy_url
    otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
    logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
    logs_collector_public_ip     = var.logs_proxy_address
    orchestrator_services        = "template-manager"
    allow_sandbox_internet       = var.allow_sandbox_internet
  })
}
resource "nomad_job" "loki" {
  jobspec = templatefile("${path.module}/loki.hcl", {
    gcp_zone = var.gcp_zone

    // We use colocation 2 here to ensure that there are at least 2 nodes for API to do rolling updates.
    // It might be possible there could be problems if we are rolling updates for both API and Loki at the same time., so maybe increasing this to > 3 makes sense.
    prevent_colocation = var.api_machine_count > 2
    loki_bucket_name   = var.loki_bucket_name

    memory_mb                = var.loki_resources_memory_mb
    cpu_count                = var.loki_resources_cpu_count
    loki_service_port_number = var.loki_service_port.port
    loki_service_port_name   = var.loki_service_port.name
  })
}

resource "nomad_job" "template-cache" {
  jobspec = templatefile("${path.module}/template-cache.hcl", {
    gcp_zone           = var.gcp_zone
    port_number        = var.template_cache_port.port
    status_port_number = var.template_cache_port.status_port
    port_name          = var.template_cache_port.name
  })
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


resource "nomad_job" "clickhouse" {
  count = var.clickhouse_server_count > 0 ? 1 : 0
  jobspec = templatefile("${path.module}/clickhouse.hcl", {
    server_secret      = random_password.clickhouse_server_secret.result
    clickhouse_version = "25.4.5.24"

    cpu_count = var.clickhouse_resources_cpu_count
    memory_mb = var.clickhouse_resources_memory_mb

    username                = var.clickhouse_username
    password                = random_password.clickhouse_password.result
    clickhouse_metrics_port = var.clickhouse_metrics_port
    clickhouse_server_port  = var.clickhouse_server_port.port
    server_count            = var.clickhouse_server_count
    resources_memory_gib    = 8

    otel_agent_config = templatefile("${path.module}/configs/clickhouse-otel-agent.yaml", {
      clickhouse_metrics_port  = var.clickhouse_metrics_port
      otel_collector_grpc_port = var.otel_collector_grpc_port
    })

    job_constraint_prefix = var.clickhouse_job_constraint_prefix
    node_pool             = var.clickhouse_node_pool
  })
}

resource "google_service_account_key" "clickhouse_service_account_key" {
  service_account_id = google_service_account.clickhouse_service_account.id
}


resource "nomad_job" "clickhouse_backup" {
  count = var.clickhouse_server_count > 0 ? 1 : 0
  jobspec = templatefile("${path.module}/clickhouse-backup.hcl", {
    clickhouse_backup_version = "2.6.22"

    gcs_bucket                   = var.clickhouse_backups_bucket_name
    gcs_folder                   = "clickhouse-data"
    gcs_credentials_json_encoded = google_service_account_key.clickhouse_service_account_key.private_key

    server_count        = var.clickhouse_server_count
    clickhouse_username = var.clickhouse_username
    clickhouse_password = random_password.clickhouse_password.result
    clickhouse_port     = var.clickhouse_server_port.port

    job_constraint_prefix = var.clickhouse_job_constraint_prefix
    node_pool             = var.clickhouse_node_pool
  })
}

resource "nomad_job" "clickhouse_backup_restore" {
  count = var.clickhouse_server_count > 0 ? 1 : 0
  jobspec = templatefile("${path.module}/clickhouse-backup-restore.hcl", {
    clickhouse_backup_version = "2.6.22"

    gcs_bucket                   = var.clickhouse_backups_bucket_name
    gcs_folder                   = "clickhouse-data"
    gcs_credentials_json_encoded = google_service_account_key.clickhouse_service_account_key.private_key

    server_count        = var.clickhouse_server_count
    clickhouse_username = var.clickhouse_username
    clickhouse_password = random_password.clickhouse_password.result
    clickhouse_port     = var.clickhouse_server_port.port

    job_constraint_prefix = var.clickhouse_job_constraint_prefix
    node_pool             = var.clickhouse_node_pool
  })
}


data "docker_registry_image" "clickhouse_migrator_image" {
  name = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${var.orchestration_repository_name}/clickhouse-migrator:latest"
}

resource "docker_image" "clickhouse_migrator_image" {
  name          = data.docker_registry_image.clickhouse_migrator_image.name
  pull_triggers = [data.docker_registry_image.clickhouse_migrator_image.sha256_digest]
  platform      = "linux/amd64/v8"
}


resource "nomad_job" "clickhouse_migrator" {
  count = var.clickhouse_server_count > 0 ? 1 : 0
  jobspec = templatefile("${path.module}/clickhouse-migrator.hcl", {
    clickhouse_migrator_version = docker_image.clickhouse_migrator_image.repo_digest

    server_count          = var.clickhouse_server_count
    job_constraint_prefix = var.clickhouse_job_constraint_prefix
    node_pool             = var.clickhouse_node_pool

    clickhouse_username = var.clickhouse_username
    clickhouse_password = random_password.clickhouse_password.result
    clickhouse_port     = var.clickhouse_server_port.port
  })
}
