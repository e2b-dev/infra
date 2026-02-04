
resource "google_secret_manager_secret" "cloudflare_api_token" {
  secret_id = "${var.prefix}cloudflare-api-token"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "consul_acl_token" {
  secret_id = "${var.prefix}consul-secret-id"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "random_uuid" "consul_acl_token" {}

resource "google_secret_manager_secret_version" "consul_acl_token" {
  secret      = google_secret_manager_secret.consul_acl_token.name
  secret_data = random_uuid.consul_acl_token.result
}

resource "google_secret_manager_secret" "nomad_acl_token" {
  secret_id = "${var.prefix}nomad-secret-id"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "random_uuid" "nomad_acl_token" {}

resource "google_secret_manager_secret_version" "nomad_acl_token" {
  secret      = google_secret_manager_secret.nomad_acl_token.name
  secret_data = random_uuid.nomad_acl_token.result
}



# grafana api key
resource "google_secret_manager_secret" "grafana_api_key" {
  secret_id = "${var.prefix}grafana-api-key"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "launch_darkly_api_key" {
  secret_id = "${var.prefix}launch-darkly-api-key"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "launch_darkly_api_key" {
  secret      = google_secret_manager_secret.launch_darkly_api_key.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "grafana_api_key" {
  secret      = google_secret_manager_secret.grafana_api_key.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}
resource "google_secret_manager_secret" "analytics_collector_host" {
  secret_id = "${var.prefix}analytics-collector-host"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "analytics_collector_host" {
  secret      = google_secret_manager_secret.analytics_collector_host.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "analytics_collector_api_token" {
  secret_id = "${var.prefix}analytics-collector-api-token"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "analytics_collector_api_token" {
  secret      = google_secret_manager_secret.analytics_collector_api_token.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "routing_domains" {
  secret_id = "${var.prefix}routing-domains"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "routing_domains" {
  secret      = google_secret_manager_secret.routing_domains.name
  secret_data = jsonencode([])

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "postgres_connection_string" {
  secret_id = "${var.prefix}postgres-connection-string"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "supabase_jwt_secrets" {
  secret_id = "${var.prefix}supabase-jwt-secrets"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "supabase_jwt_secrets" {
  secret      = google_secret_manager_secret.supabase_jwt_secrets.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}


resource "google_secret_manager_secret" "posthog_api_key" {
  secret_id = "${var.prefix}posthog-api-key"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "posthog_api_key" {
  secret      = google_secret_manager_secret.posthog_api_key.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}


resource "google_secret_manager_secret" "redis_cluster_url" {
  secret_id = "${var.prefix}redis-cluster-url"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "redis_cluster_url" {
  secret      = google_secret_manager_secret.redis_cluster_url.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "redis_tls_ca_base64" {
  secret_id = "${var.prefix}redis-tls-ca-base64"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "redis_tls_ca_base64" {
  secret      = google_secret_manager_secret.redis_tls_ca_base64.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}


resource "google_secret_manager_secret" "notification_email" {
  secret_id = "${var.prefix}security-notification-email"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "notification_email_value" {
  secret = google_secret_manager_secret.notification_email.id

  secret_data = "placeholder@example.com"
}
