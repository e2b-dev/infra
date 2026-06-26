data "google_artifact_registry_docker_image" "api_image" {
  location      = var.gcp_region
  repository_id = var.core_repository_name
  image_name    = "api:latest"
}
