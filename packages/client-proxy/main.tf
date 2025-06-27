resource "random_password" "edge_api_secret" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "edge_api_secret" {
  secret_id = "${var.prefix}edge-api-secret"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "edge_api_secret" {
  secret      = google_secret_manager_secret.edge_api_secret.id
  secret_data = random_password.edge_api_secret.result
}