resource "google_secret_manager_secret" "postgres_connection_string" {
  secret_id = "${var.prefix}postgres-connection-string"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret" "supabase_jwt_secrets" {
  secret_id = "${var.prefix}supabase-jwt-secrets"

  replication {
    auto {}
  }
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
