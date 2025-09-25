# Spanner instance for Vault backend storage
resource "google_spanner_instance" "vault" {
  name             = "${var.prefix}${var.vault_spanner_instance_name}"
  config           = "regional-${var.gcp_region}"
  display_name     = "Vault Backend Storage"
  processing_units = var.vault_spanner_processing_units
  labels           = var.labels

  lifecycle {
    prevent_destroy = true
  }
}

# Spanner database for Vault backend storage
resource "google_spanner_database" "vault" {
  instance                 = google_spanner_instance.vault.name
  name                     = var.vault_spanner_database_name
  version_retention_period = "3d"
  deletion_protection      = true
  ddl = [
    "CREATE TABLE Vault (Key STRING(MAX) NOT NULL, Value BYTES(MAX)) PRIMARY KEY (Key)",
    "CREATE TABLE VaultHA (Key STRING(MAX) NOT NULL, Value STRING(MAX), Identity STRING(36) NOT NULL, Timestamp TIMESTAMP NOT NULL) PRIMARY KEY (Key)"
  ]

  lifecycle {
    prevent_destroy = true
  }
}

# Output the Spanner instance and database information for Vault configuration
output "vault_spanner_instance_name" {
  description = "Name of the Spanner instance for Vault backend"
  value       = google_spanner_instance.vault.name
}

output "vault_spanner_database_name" {
  description = "Name of the Spanner database for Vault backend"
  value       = google_spanner_database.vault.name
}


# IAM binding for infra service account to access Spanner database
resource "google_spanner_database_iam_member" "infra_instances_service_account" {
  project  = var.gcp_project_id
  instance = google_spanner_instance.vault.name
  database = google_spanner_database.vault.name
  role     = "roles/spanner.databaseUser"
  member   = "serviceAccount:${module.init.service_account_email}"
}

output "vault_spanner_database_path" {
  description = "Full path to the Spanner database for Vault backend"
  value       = "projects/${var.gcp_project_id}/instances/${google_spanner_instance.vault.name}/databases/${google_spanner_database.vault.name}"
}
