terraform {
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "3.0.2"
    }
  }
}

data "docker_registry_image" "proxy_image" {
  name = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${var.orchestration_repository_name}/client-proxy"
}

resource "docker_image" "client_proxy_image" {
  name          = data.docker_registry_image.proxy_image.name
  pull_triggers = [data.docker_registry_image.proxy_image.sha256_digest]
  platform      = "linux/amd64/v8"
}

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