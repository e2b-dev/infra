output "docker_remote_repository_url" {
  value = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${google_artifact_registry_repository.docker_remote_repository.name}"
}
