// ---
// Clickhouse
// ---
resource "aws_secretsmanager_secret" "clickhouse" {
  name = "${var.prefix}clickhouse"
}

resource "random_string" "clickhouse_password" {
  length  = 32
  special = false
}

resource "random_string" "clickhouse_server_secret" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret_version" "clickhouse_initial" {
  secret_id = aws_secretsmanager_secret.clickhouse.id
  secret_string = jsonencode({
    "CLICKHOUSE_USERNAME" = "e2b",
    "CLICKHOUSE_PASSWORD" = random_string.clickhouse_password.result,
    "SERVER_SECRET"       = random_string.clickhouse_server_secret.result,
  })

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "clickhouse" {
  secret_id     = aws_secretsmanager_secret.clickhouse.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.clickhouse_initial]
}

locals {
  clickhouse_raw = jsondecode(data.aws_secretsmanager_secret_version.clickhouse.secret_string)
}

output "clickhouse" {
  value = {
    username      = local.clickhouse_raw["CLICKHOUSE_USERNAME"]
    password      = local.clickhouse_raw["CLICKHOUSE_PASSWORD"]
    server_secret = local.clickhouse_raw["SERVER_SECRET"]
  }
}

// ---
// Grafana
// ---
resource "aws_secretsmanager_secret" "grafana" {
  name = "${var.prefix}grafana"
}

resource "aws_secretsmanager_secret_version" "grafana" {
  secret_id = aws_secretsmanager_secret.grafana.id
  secret_string = jsonencode({
    "API_KEY"                  = " ",
    "OTLP_URL"                 = " ",
    "OTEL_COLLECTOR_TOKEN"     = " ",
    "USERNAME"                 = " ",
    "LOGS_USER"                = " ",
    "LOGS_URL"                 = " ",
    "LOGS_COLLECTOR_API_TOKEN" = " ",
  })

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana" {
  secret_id     = aws_secretsmanager_secret.grafana.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.grafana]
}

locals {
  grafana_raw = jsondecode(data.aws_secretsmanager_secret_version.grafana.secret_string)
}

output "grafana" {
  value = {
    api_key                  = local.grafana_raw["API_KEY"]
    otlp_url                 = local.grafana_raw["OTLP_URL"]
    otel_collector_token     = local.grafana_raw["OTEL_COLLECTOR_TOKEN"]
    username                 = local.grafana_raw["USERNAME"]
    logs_user                = local.grafana_raw["LOGS_USER"]
    logs_url                 = local.grafana_raw["LOGS_URL"]
    logs_collector_api_token = local.grafana_raw["LOGS_COLLECTOR_API_TOKEN"]
  }
  sensitive = true
}

// ---
// API Secret
// ---
resource "random_string" "api_secret" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "api_secret" {
  name = "${var.prefix}api-secret"
}

resource "aws_secretsmanager_secret_version" "api_secret" {
  secret_id     = aws_secretsmanager_secret.api_secret.id
  secret_string = random_string.api_secret.result

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "api_secret" {
  secret_id     = aws_secretsmanager_secret.api_secret.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.api_secret]
}

output "api_secret" {
  value     = data.aws_secretsmanager_secret_version.api_secret.secret_string
  sensitive = true
}

// ---
// Launch Darkly
// ---
resource "aws_secretsmanager_secret" "launch_darkly_api_key" {
  name = "${var.prefix}launch-darkly-api-key"
}

resource "aws_secretsmanager_secret_version" "launch_darkly_api_key" {
  secret_id     = aws_secretsmanager_secret.launch_darkly_api_key.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "launch_darkly_api_key" {
  secret_id     = aws_secretsmanager_secret.launch_darkly_api_key.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.launch_darkly_api_key]
}

output "launch_darkly_api_key" {
  value     = data.aws_secretsmanager_secret_version.launch_darkly_api_key.secret_string
  sensitive = true
}

// ---
// PostgreSQL
// ---
resource "aws_secretsmanager_secret" "postgres_connection_string" {
  name = "${var.prefix}postgres-connection-string"
}

resource "aws_secretsmanager_secret_version" "postgres_connection_string" {
  secret_id     = aws_secretsmanager_secret.postgres_connection_string.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "postgres_connection_string" {
  secret_id     = aws_secretsmanager_secret.postgres_connection_string.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.postgres_connection_string]
}

output "postgres_connection_string_secret_name" {
  value = aws_secretsmanager_secret.postgres_connection_string.name
}

output "postgres_connection_string" {
  value     = data.aws_secretsmanager_secret_version.postgres_connection_string.secret_string
  sensitive = true
}

// ---
// Supabase
// ---
resource "aws_secretsmanager_secret" "supabase_jwt_secrets" {
  name = "${var.prefix}supabase-jwt-secrets"
}

resource "aws_secretsmanager_secret_version" "supabase_jwt_secrets" {
  secret_id     = aws_secretsmanager_secret.supabase_jwt_secrets.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "supabase_jwt_secrets" {
  secret_id     = aws_secretsmanager_secret.supabase_jwt_secrets.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.supabase_jwt_secrets]
}

output "supabase_jwt_secret_name" {
  value = aws_secretsmanager_secret.supabase_jwt_secrets.name
}

output "supabase_jwt_secrets" {
  value     = data.aws_secretsmanager_secret_version.supabase_jwt_secrets.secret_string
  sensitive = true
}

// ---
// API Admin Token
// ---
resource "random_string" "admin_token" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "admin_token" {
  name = "${var.prefix}admin-token"
}

resource "aws_secretsmanager_secret_version" "admin_token" {
  secret_id     = aws_secretsmanager_secret.admin_token.id
  secret_string = random_string.admin_token.result

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "admin_token" {
  secret_id     = aws_secretsmanager_secret.admin_token.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.admin_token]
}

output "admin_token" {
  value     = data.aws_secretsmanager_secret_version.admin_token.secret_string
  sensitive = true
}

// ---
// Sandbox Access Token Hash Seed
// ---
resource "random_string" "sandbox_access_token_hash_seed" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "sandbox_access_token_hash_seed" {
  name = "${var.prefix}sandbox-access-token-hash-seed"
}

resource "aws_secretsmanager_secret_version" "sandbox_access_token_hash_seed" {
  secret_id     = aws_secretsmanager_secret.sandbox_access_token_hash_seed.id
  secret_string = random_string.sandbox_access_token_hash_seed.result

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "sandbox_access_token_hash_seed" {
  secret_id     = aws_secretsmanager_secret.sandbox_access_token_hash_seed.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.sandbox_access_token_hash_seed]
}

output "sandbox_access_token_hash_seed" {
  value     = data.aws_secretsmanager_secret_version.sandbox_access_token_hash_seed.secret_string
  sensitive = true
}

// ---
// PostHog
// ---
resource "aws_secretsmanager_secret" "posthog_api_key" {
  name = "${var.prefix}posthog-api-key"
}

resource "aws_secretsmanager_secret_version" "posthog_api_key" {
  secret_id     = aws_secretsmanager_secret.posthog_api_key.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "posthog_api_key" {
  secret_id     = aws_secretsmanager_secret.posthog_api_key.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.posthog_api_key]
}

output "posthog_api_key" {
  value     = data.aws_secretsmanager_secret_version.posthog_api_key.secret_string
  sensitive = true
}

// ---
// Analytics Collector
// ---
resource "aws_secretsmanager_secret" "analytics_collector" {
  name = "${var.prefix}analytics-collector"
}

resource "aws_secretsmanager_secret_version" "analytics_collector" {
  secret_id = aws_secretsmanager_secret.analytics_collector.id
  secret_string = jsonencode({
    "HOST"      = " ",
    "API_TOKEN" = " ",
  })

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "analytics_collector" {
  secret_id     = aws_secretsmanager_secret.analytics_collector.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.analytics_collector]
}

locals {
  analytics_collector_raw = jsondecode(data.aws_secretsmanager_secret_version.analytics_collector.secret_string)
}

output "analytics_collector" {
  value = {
    host      = local.analytics_collector_raw["HOST"]
    api_token = local.analytics_collector_raw["API_TOKEN"]
  }
  sensitive = true
}

// ---
// Redis
// ---
resource "aws_secretsmanager_secret" "redis_cluster_url" {
  name = "${var.prefix}redis-cluster-url"
}

resource "aws_secretsmanager_secret_version" "redis_cluster_url" {
  secret_id     = aws_secretsmanager_secret.redis_cluster_url.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "redis_cluster_url" {
  secret_id     = aws_secretsmanager_secret.redis_cluster_url.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.redis_cluster_url]
}

resource "aws_secretsmanager_secret" "redis_tls_ca_base64" {
  name = "${var.prefix}redis-tls-ca-base64"
}

resource "aws_secretsmanager_secret_version" "redis_tls_ca_base64" {
  secret_id     = aws_secretsmanager_secret.redis_tls_ca_base64.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "redis_tls_ca_base64" {
  secret_id     = aws_secretsmanager_secret.redis_tls_ca_base64.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.redis_tls_ca_base64]
}

output "redis_cluster_url_secret_name" {
  value = aws_secretsmanager_secret.redis_cluster_url.name
}

output "redis_tls_ca_base64_secret_name" {
  value = aws_secretsmanager_secret.redis_tls_ca_base64.name
}

