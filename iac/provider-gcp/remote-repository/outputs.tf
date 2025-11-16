output "dockerhub_remote_repository_url" {
  value = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${google_artifact_registry_repository.dockerhub_remote_repository.name}"
}
