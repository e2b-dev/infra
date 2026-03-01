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
resource "aws_secretsmanager_secret" "grafana_api_key" {
  name = "${var.prefix}grafana-api-key"
}

resource "aws_secretsmanager_secret_version" "grafana_api_key" {
  secret_id     = aws_secretsmanager_secret.grafana_api_key.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_api_key" {
  secret_id     = aws_secretsmanager_secret.grafana_api_key.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.grafana_api_key]
}

output "grafana_api_key" {
  value     = data.aws_secretsmanager_secret_version.grafana_api_key.secret_string
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

