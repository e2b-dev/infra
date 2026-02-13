data "google_artifact_registry_docker_image" "api_image" {
  location      = var.gcp_region
  repository_id = var.core_repository_name
  image_name    = "api:latest"
}

data "google_artifact_registry_docker_image" "db_migrator_image" {
  location      = var.gcp_region
  image_name    = "db-migrator:latest"
  repository_id = var.core_repository_name
}

data "google_artifact_registry_docker_image" "docker_reverse_proxy_image" {
  location      = var.gcp_region
  image_name    = "docker-reverse-proxy:latest"
  repository_id = var.core_repository_name
}

data "google_artifact_registry_docker_image" "client_proxy_image" {
  location      = var.gcp_region
  image_name    = "client-proxy:latest"
  repository_id = var.core_repository_name
}

data "google_artifact_registry_docker_image" "clickhouse_migrator_image" {
  location      = var.gcp_region
  image_name    = "clickhouse-migrator:latest"
  repository_id = var.core_repository_name
}
