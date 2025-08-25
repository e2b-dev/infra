
# Enable Secrets Manager API
resource "google_project_service" "secrets_manager_api" {
  service = "secretmanager.googleapis.com"

  disable_on_destroy = false
}

# Enable Cloud KMS API
resource "google_project_service" "cloudkms_api" {
  service = "cloudkms.googleapis.com"

  disable_on_destroy = false
}

# Enable Certificate Manager API
resource "google_project_service" "certificate_manager_api" {
  #project = var.gcp_project_id
  service = "certificatemanager.googleapis.com"

  disable_on_destroy = false
}

# Enable Compute Engine API
resource "google_project_service" "compute_engine_api" {
  #project = var.gcp_project_id
  service = "compute.googleapis.com"

  disable_on_destroy = false
}

# Enable Artifact Registry API
resource "google_project_service" "artifact_registry_api" {
  #project = var.gcp_project_id
  service = "artifactregistry.googleapis.com"

  disable_on_destroy = false
}

# Enable OS Config API
resource "google_project_service" "os_config_api" {
  #project = var.gcp_project_id
  service = "osconfig.googleapis.com"

  disable_on_destroy = false
}

# Enable Stackdriver Monitoring API
resource "google_project_service" "monitoring_api" {
  #project = var.gcp_project_id
  service = "monitoring.googleapis.com"

  disable_on_destroy = false
}

# Enable Stackdriver Logging API
resource "google_project_service" "logging_api" {
  #project = var.gcp_project_id
  service = "logging.googleapis.com"

  disable_on_destroy = false
}

resource "time_sleep" "secrets_api_wait_60_seconds" {
  depends_on = [google_project_service.secrets_manager_api]

  create_duration = "20s"
}

resource "google_service_account" "infra_instances_service_account" {
  account_id   = "${var.prefix}infra-instances"
  display_name = "Infra Instances Service Account"
}

resource "google_service_account_key" "google_service_key" {
  service_account_id = google_service_account.infra_instances_service_account.name
}


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

resource "google_artifact_registry_repository" "orchestration_repository" {
  format        = "DOCKER"
  repository_id = "e2b-orchestration"
  labels        = var.labels

  depends_on = [time_sleep.artifact_registry_api_wait_90_seconds]
}

resource "time_sleep" "artifact_registry_api_wait_90_seconds" {
  depends_on = [google_project_service.artifact_registry_api]

  create_duration = "90s"
}


resource "google_artifact_registry_repository_iam_member" "orchestration_repository_member" {
  repository = google_artifact_registry_repository.orchestration_repository.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.infra_instances_service_account.email}"

  depends_on = [time_sleep.artifact_registry_api_wait_90_seconds]
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

# GCP KMS resources for Vault auto-unseal
resource "google_kms_key_ring" "vault" {
  name     = "${var.prefix}vault-keyring"
  location = var.gcp_region
  project  = var.gcp_project_id
}

resource "google_kms_crypto_key" "vault_unseal" {
  name            = "${var.prefix}vault-unseal-key"
  key_ring        = google_kms_key_ring.vault.id
  rotation_period = "7776000s" # 90 days
  # https://developer.hashicorp.com/vault/docs/configuration/seal/gcpckms#key-rotation
  # Auto rotating kms key is fine, just *never* delete old versions or you will break the vault

  lifecycle {
    prevent_destroy = true
  }
}

# Get the default compute service account
data "google_compute_default_service_account" "default" {
  project = var.gcp_project_id
}

# Grant the compute service account permissions to use the KMS key
resource "google_kms_crypto_key_iam_member" "vault_unseal" {
  crypto_key_id = google_kms_crypto_key.vault_unseal.id
  role          = "roles/cloudkms.cryptoKeyEncrypterDecrypter"
  member        = "serviceAccount:${data.google_compute_default_service_account.default.email}"
}

# Grant the compute service account permissions to view the KMS key
resource "google_kms_crypto_key_iam_member" "vault_unseal_viewer" {
  crypto_key_id = google_kms_crypto_key.vault_unseal.id
  role          = "roles/cloudkms.viewer"
  member        = "serviceAccount:${data.google_compute_default_service_account.default.email}"
}

# Grant the infra instances service account permissions to use the KMS key
resource "google_kms_crypto_key_iam_member" "vault_unseal_infra_instances" {
  crypto_key_id = google_kms_crypto_key.vault_unseal.id
  role          = "roles/cloudkms.cryptoKeyEncrypterDecrypter"
  member        = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

# Grant the infra instances service account permissions to view the KMS key
resource "google_kms_crypto_key_iam_member" "vault_unseal_infra_instances_viewer" {
  crypto_key_id = google_kms_crypto_key.vault_unseal.id
  role          = "roles/cloudkms.viewer"
  member        = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

# Secret for storing Vault root keys
resource "google_secret_manager_secret" "vault_root_key" {
  secret_id = "${var.prefix}vault-root-key"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "vault_root_key" {
  secret      = google_secret_manager_secret.vault_root_key.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

}

# Secret for storing Vault recovery keys
resource "google_secret_manager_secret" "vault_recovery_keys" {
  secret_id = "${var.prefix}vault-recovery-keys"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "vault_recovery_keys" {
  secret = google_secret_manager_secret.vault_recovery_keys.name
  secret_data = jsonencode({
    recovery_keys = []
  })

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# Secret for storing Vault API service AppRole credentials
resource "google_secret_manager_secret" "vault_api_approle" {
  secret_id = "${var.prefix}vault-api-approle"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "vault_api_approle" {
  secret = google_secret_manager_secret.vault_api_approle.name
  secret_data = jsonencode({
    role_id     = ""
    secret_id   = ""
    role        = "api-service"
    permissions = ["write", "delete"]
  })

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# Secret for storing Vault Orchestrator service AppRole credentials
resource "google_secret_manager_secret" "vault_orchestrator_approle" {
  secret_id = "${var.prefix}vault-orchestrator-approle"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "vault_orchestrator_approle" {
  secret = google_secret_manager_secret.vault_orchestrator_approle.name
  secret_data = jsonencode({
    role_id     = ""
    secret_id   = ""
    role        = "orchestrator-service"
    permissions = ["read"]
  })

  lifecycle {
    ignore_changes = [secret_data]
  }
}
