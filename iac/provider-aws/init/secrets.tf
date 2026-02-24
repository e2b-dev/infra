# --- Auto-generated secrets ---

resource "aws_secretsmanager_secret" "consul_acl_token" {
  name = "${var.prefix}consul-secret-id"
  tags = var.tags
}

resource "random_uuid" "consul_acl_token" {}

resource "aws_secretsmanager_secret_version" "consul_acl_token" {
  secret_id     = aws_secretsmanager_secret.consul_acl_token.id
  secret_string = random_uuid.consul_acl_token.result
}

resource "aws_secretsmanager_secret" "nomad_acl_token" {
  name = "${var.prefix}nomad-secret-id"
  tags = var.tags
}

resource "random_uuid" "nomad_acl_token" {}

resource "aws_secretsmanager_secret_version" "nomad_acl_token" {
  secret_id     = aws_secretsmanager_secret.nomad_acl_token.id
  secret_string = random_uuid.nomad_acl_token.result
}

# --- Placeholder secrets (user must populate after deployment) ---

resource "aws_secretsmanager_secret" "cloudflare_api_token" {
  name = "${var.prefix}cloudflare-api-token"
  tags = var.tags
}

resource "aws_secretsmanager_secret" "grafana_api_key" {
  name = "${var.prefix}grafana-api-key"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "grafana_api_key" {
  secret_id     = aws_secretsmanager_secret.grafana_api_key.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "launch_darkly_api_key" {
  name = "${var.prefix}launch-darkly-api-key"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "launch_darkly_api_key" {
  secret_id     = aws_secretsmanager_secret.launch_darkly_api_key.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "postgres_connection_string" {
  name = "${var.prefix}postgres-connection-string"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "postgres_connection_string" {
  secret_id     = aws_secretsmanager_secret.postgres_connection_string.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "postgres_read_replica_connection_string" {
  name = "${var.prefix}postgres-read-replica-connection-string"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "postgres_read_replica_connection_string" {
  secret_id     = aws_secretsmanager_secret.postgres_read_replica_connection_string.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "supabase_jwt_secrets" {
  name = "${var.prefix}supabase-jwt-secrets"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "supabase_jwt_secrets" {
  secret_id     = aws_secretsmanager_secret.supabase_jwt_secrets.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "posthog_api_key" {
  name = "${var.prefix}posthog-api-key"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "posthog_api_key" {
  secret_id     = aws_secretsmanager_secret.posthog_api_key.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "analytics_collector_host" {
  name = "${var.prefix}analytics-collector-host"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "analytics_collector_host" {
  secret_id     = aws_secretsmanager_secret.analytics_collector_host.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "analytics_collector_api_token" {
  name = "${var.prefix}analytics-collector-api-token"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "analytics_collector_api_token" {
  secret_id     = aws_secretsmanager_secret.analytics_collector_api_token.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "routing_domains" {
  name = "${var.prefix}routing-domains"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "routing_domains" {
  secret_id     = aws_secretsmanager_secret.routing_domains.id
  secret_string = jsonencode([])

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "redis_cluster_url" {
  name = "${var.prefix}redis-cluster-url"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "redis_cluster_url" {
  secret_id     = aws_secretsmanager_secret.redis_cluster_url.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "redis_tls_ca_base64" {
  name = "${var.prefix}redis-tls-ca-base64"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "redis_tls_ca_base64" {
  secret_id     = aws_secretsmanager_secret.redis_tls_ca_base64.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "dockerhub_username" {
  name = "${var.prefix}dockerhub-remote-repo-username"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "dockerhub_username" {
  secret_id     = aws_secretsmanager_secret.dockerhub_username.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "dockerhub_password" {
  name = "${var.prefix}dockerhub-remote-repo-password"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "dockerhub_password" {
  secret_id     = aws_secretsmanager_secret.dockerhub_password.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}
