output "service_account_email" {
  value = google_service_account.infra_instances_service_account.email
}

output "google_service_account_key" {
  value = google_service_account_key.google_service_key.private_key
}

output "consul_acl_token_secret" {
  value = google_secret_manager_secret_version.consul_acl_token.secret_data
}

output "nomad_acl_token_secret" {
  value = google_secret_manager_secret_version.nomad_acl_token.secret_data
}

output "grafana_api_key_secret_name" {
  value = google_secret_manager_secret.grafana_api_key.name
}

output "grafana_username_secret_name" {
  value = google_secret_manager_secret.grafana_username.name
}

output "grafana_logs_username_secret_name" {
  value = google_secret_manager_secret.grafana_logs_username.name
}

output "analytics_collector_host_secret_name" {
  value = google_secret_manager_secret.analytics_collector_host.name
}

output "analytics_collector_api_token_secret_name" {
  value = google_secret_manager_secret.analytics_collector_api_token.name
}

output "orchestration_repository_name" {
  value = google_artifact_registry_repository.orchestration_repository.name
}

output "cloudflare_api_token_secret_name" {
  value = google_secret_manager_secret.cloudflare_api_token.name
}

output "grafana_otel_collector_token_secret_name" {
  value = google_secret_manager_secret.grafana_otel_collector_token.name
}

output "grafana_api_key_logs_collector_secret_name" {
  value = google_secret_manager_secret.grafana_logs_collector_api_token.name
}

output "grafana_logs_url_secret_name" {
  value = google_secret_manager_secret.grafana_logs_url.name
}
