locals {
  cloud_sql_database_name = "e2b"
  cloud_sql_user_name     = "e2b"
}

data "google_compute_network" "workload" {
  name    = var.network_name
  project = var.gcp_project_id
}

resource "google_project_service" "cloud_sql_admin_api" {
  project = var.gcp_project_id
  service = "sqladmin.googleapis.com"

  disable_on_destroy = false
}

resource "google_project_service" "service_networking_api" {
  project = var.gcp_project_id
  service = "servicenetworking.googleapis.com"

  disable_on_destroy = false
}

resource "google_project_service_identity" "cloud_sql" {
  provider = google-beta

  project = var.gcp_project_id
  service = google_project_service.cloud_sql_admin_api.service
}

resource "google_project_service_identity" "service_networking" {
  provider = google-beta

  project = var.gcp_project_id
  service = google_project_service.service_networking_api.service
}

resource "time_sleep" "service_identity_propagation" {
  create_duration = "30s"

  depends_on = [
    google_project_service_identity.cloud_sql,
    google_project_service_identity.service_networking,
  ]
}

resource "google_project_iam_member" "cloud_sql_service_agent" {
  project = var.gcp_project_id
  role    = "roles/cloudsql.serviceAgent"
  member  = "serviceAccount:${google_project_service_identity.cloud_sql.email}"

  depends_on = [
    time_sleep.service_identity_propagation,
  ]
}

resource "google_project_iam_member" "service_networking_service_agent" {
  project = var.gcp_project_id
  role    = "roles/servicenetworking.serviceAgent"
  member  = "serviceAccount:${google_project_service_identity.service_networking.email}"

  depends_on = [
    time_sleep.service_identity_propagation,
  ]
}

resource "google_compute_global_address" "cloud_sql_private_services" {
  name          = "${var.prefix}cloud-sql-private-services"
  project       = var.gcp_project_id
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 24
  network       = data.google_compute_network.workload.id

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [
    google_project_iam_member.service_networking_service_agent,
  ]
}

resource "google_service_networking_connection" "cloud_sql" {
  network                 = data.google_compute_network.workload.id
  service                 = google_project_service.service_networking_api.service
  reserved_peering_ranges = [google_compute_global_address.cloud_sql_private_services.name]
  deletion_policy         = "ABANDON"

  depends_on = [
    google_project_iam_member.service_networking_service_agent,
  ]
}

resource "google_sql_database_instance" "operator_canary" {
  name             = "${var.prefix}postgres-canary"
  project          = var.gcp_project_id
  region           = var.gcp_region
  database_version = "POSTGRES_16"

  deletion_protection = true

  settings {
    tier              = "db-custom-2-7680"
    edition           = "ENTERPRISE"
    availability_type = "REGIONAL"

    disk_type             = "PD_SSD"
    disk_size             = 20
    disk_autoresize       = true
    disk_autoresize_limit = 200

    deletion_protection_enabled = true
    user_labels                 = var.labels

    backup_configuration {
      enabled                        = true
      location                       = var.gcp_region
      point_in_time_recovery_enabled = true
      start_time                     = "03:00"
      transaction_log_retention_days = 7

      backup_retention_settings {
        retained_backups = 7
        retention_unit   = "COUNT"
      }
    }

    ip_configuration {
      ipv4_enabled                                  = false
      private_network                               = data.google_compute_network.workload.id
      allocated_ip_range                            = google_compute_global_address.cloud_sql_private_services.name
      enable_private_path_for_google_cloud_services = false
      ssl_mode                                      = "ENCRYPTED_ONLY"
    }
  }

  depends_on = [
    google_project_iam_member.cloud_sql_service_agent,
    google_service_networking_connection.cloud_sql,
    terraform_data.cloud_sql_connection_budget,
  ]
}

resource "terraform_data" "cloud_sql_connection_budget" {
  input = {
    api_server_count                                = var.api_server_count
    dashboard_api_count                             = var.dashboard_api_count
    db_max_open_connections                         = var.db_max_open_connections
    db_min_idle_connections                         = var.db_min_idle_connections
    auth_db_max_open_connections                    = var.auth_db_max_open_connections
    auth_db_min_idle_connections                    = var.auth_db_min_idle_connections
    migrator_max_open_connections                   = 4
    docker_reverse_proxy_max_open_connections       = 6
    dashboard_api_max_open_connections_per_instance = 16
    maximum_concurrent_connections                  = (var.db_max_open_connections + var.auth_db_max_open_connections) * var.api_server_count + 16 * var.dashboard_api_count + 6 + 4
  }

  lifecycle {
    precondition {
      condition = (
        var.db_max_open_connections >= 1
        && var.auth_db_max_open_connections >= 1
        && var.db_min_idle_connections >= 0
        && var.db_min_idle_connections <= var.db_max_open_connections
        && var.auth_db_min_idle_connections >= 0
        && var.auth_db_min_idle_connections <= var.auth_db_max_open_connections
        && var.api_server_count == 2
        && var.dashboard_api_count == 0
        && (var.db_max_open_connections + var.auth_db_max_open_connections) * var.api_server_count + 16 * var.dashboard_api_count + 6 + 4 <= 100
      )
      error_message = <<-EOT
        The invited-beta regional Cloud SQL contract requires exactly two API
        allocations, no dashboard API, valid idle bounds, and at most 100
        configured API, reverse-proxy, dashboard, and migrator connections in
        aggregate. Update the reviewed workload policy before increasing this
        conservative application-side budget or replica count.
      EOT
    }
  }
}

resource "google_sql_database" "operator_canary" {
  name     = local.cloud_sql_database_name
  project  = var.gcp_project_id
  instance = google_sql_database_instance.operator_canary.name
}

resource "random_password" "cloud_sql_operator_canary" {
  length  = 32
  special = false
}

resource "google_sql_user" "operator_canary" {
  name     = local.cloud_sql_user_name
  project  = var.gcp_project_id
  instance = google_sql_database_instance.operator_canary.name
  password = random_password.cloud_sql_operator_canary.result
}

resource "google_secret_manager_secret_version" "postgres_connection_string" {
  secret = module.init.postgres_connection_string_secret_name
  secret_data = format(
    "postgresql://%s:%s@%s:5432/%s?sslmode=require",
    google_sql_user.operator_canary.name,
    random_password.cloud_sql_operator_canary.result,
    google_sql_database_instance.operator_canary.private_ip_address,
    google_sql_database.operator_canary.name,
  )

  depends_on = [
    google_sql_database.operator_canary,
    google_sql_user.operator_canary,
  ]
}
