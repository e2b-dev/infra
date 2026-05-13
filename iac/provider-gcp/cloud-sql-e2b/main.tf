# =============================================================================
# Manifest fork addition — e2b control-plane Postgres on Cloud SQL
# =============================================================================
#
# Upstream e2b expects an external Postgres (Supabase per self-host.md). We
# provision it ourselves in `manifest-harness` GCP so all control-plane data
# stays in our project. Private IP only, on the existing `harness-vpc` (which
# is created and shared by `manifestlaw-labs/ManifestOS` → infrastructure/
# harness-platform/). The peering range `harness-vpc-private-service-range` +
# service-networking connection are already provisioned by harness-platform —
# Cloud SQL just consumes from them, so we don't recreate either resource
# here (would conflict).
#
# Connection string is pushed into the existing
# `${var.prefix}postgres-connection-string` Secret Manager container (created
# by module.init) — the rest of the fork's plumbing reads from that secret
# unchanged.
#
# Single shared instance (not per-env) — same as the rest of e2b's control
# plane in this fork.
# =============================================================================

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
    random = {
      source = "hashicorp/random"
    }
  }
}

resource "random_password" "e2b_db" {
  length  = 32
  special = true

  # URL-safe only (RFC 3986 unreserved minus alphanumerics): `_` and `-`. Any
  # other "special" char (`@`, `%`, `#`, `&`, `(`, `)`, `*`, `!`, `^`) needs
  # percent-encoding inside a postgres:// userinfo, which random_password
  # cannot produce and e2b's pgx parser then rejects. We trade a tiny bit of
  # entropy for an unambiguous, always-valid URL.
  override_special = "_-"

  keepers = {
    instance_name = "${var.prefix}db"
  }
}

resource "google_sql_database_instance" "e2b" {
  name             = "${var.prefix}db"
  database_version = "POSTGRES_15"
  region           = var.gcp_region
  project          = var.gcp_project_id

  # Match the rest of the e2b fork's deletion-protection posture — explicit
  # operator action required to tear down the control-plane DB.
  deletion_protection = true

  settings {
    tier              = "db-custom-1-3840"
    availability_type = "ZONAL"
    edition           = "ENTERPRISE"

    disk_size       = 20
    disk_type       = "PD_SSD"
    disk_autoresize = true

    ip_configuration {
      ipv4_enabled    = false
      private_network = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
      ssl_mode        = "ENCRYPTED_ONLY"

      enable_private_path_for_google_cloud_services = true
    }

    backup_configuration {
      enabled                        = true
      start_time                     = "04:00"
      location                       = "us"
      point_in_time_recovery_enabled = false

      backup_retention_settings {
        retained_backups = 7
        retention_unit   = "COUNT"
      }
    }

    database_flags {
      name  = "cloudsql.iam_authentication"
      value = "on"
    }

    user_labels = merge(var.labels, {
      component = "e2b-control-plane-db"
    })
  }

  lifecycle {
    prevent_destroy = true

    # disk_autoresize will grow the disk past Terraform's known value;
    # ignore so subsequent plans stay clean.
    ignore_changes = [settings[0].disk_size]
  }
}

resource "google_sql_database" "e2b" {
  name     = "e2b"
  instance = google_sql_database_instance.e2b.name
  project  = var.gcp_project_id
}

## Why we use the built-in `postgres` user, not a dedicated `e2b` user:
##
## e2b's migration 20231220094836_create_triggers_and_policies.sql does
## `CREATE USER trigger_user; GRANT trigger_user TO postgres;` and then
## reassigns several function OWNERs to trigger_user. PostgreSQL requires the
## ALTER FUNCTION ... OWNER TO caller to be a member of the target role. A
## dedicated `e2b` user is NOT in trigger_user → migration fails midway with
## SQLSTATE 42501 ("must be member of role 'trigger_user'"). The upstream
## guide implicitly assumes Supabase's default `postgres` user is doing the
## migrations — we mirror that here by setting a managed password on the
## Cloud SQL built-in `postgres` user and using it as the connection identity.
resource "google_sql_user" "postgres" {
  name     = "postgres"
  instance = google_sql_database_instance.e2b.name
  project  = var.gcp_project_id
  password = random_password.e2b_db.result
}

# Connection string published to the Secret Manager container created by
# module.init (`${var.prefix}postgres-connection-string`). Cloud SQL Auth
# Proxy is NOT used here — e2b services connect via private IP directly.
resource "google_secret_manager_secret_version" "e2b_postgres_connection_string" {
  secret      = var.postgres_connection_string_secret_name
  secret_data = "postgresql://postgres:${random_password.e2b_db.result}@${google_sql_database_instance.e2b.private_ip_address}:5432/e2b?sslmode=require"
}
