output "api_docker_image_digest" {
  value = docker_image.api_image.repo_digest
}

output "api_secret" {
  value = random_password.api_secret.result
}

output "postgres_connection_string_secret_name" {
  value = google_secret_manager_secret.postgres_connection_string.name
}

output "supabase_jwt_secrets_secret_name" {
  value = google_secret_manager_secret.supabase_jwt_secrets.name
}

output "redis_url_secret_version" {
  value = google_secret_manager_secret_version.redis_url
}

output "posthog_api_key_secret_name" {
  value = google_secret_manager_secret.posthog_api_key.name
}

output "custom_envs_repository_name" {
  value = google_artifact_registry_repository.custom_environments_repository.name
}

output "api_admin_token" {
  value = random_password.api_admin_secret.result
}