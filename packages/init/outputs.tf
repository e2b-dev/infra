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
  value = google_secret_manager_secret_version.grafana_api_key.secret_data
}

output "grafana_logs_username_secret_name" {
  value = google_secret_manager_secret_version.grafana_logs_username.secret_data
}

output "grafana_traces_username_secret_name" {
  value = google_secret_manager_secret_version.grafana_traces_username.secret_data
}

output "grafana_metrics_username_secret_name" {
  value = google_secret_manager_secret_version.grafana_metrics_username.secret_data
}

output "grafana_logs_endpoint_secret_name" {
  value = google_secret_manager_secret_version.grafana_logs_endpoint.secret_data
}

output "grafana_traces_endpoint_secret_name" {
  value = google_secret_manager_secret_version.grafana_traces_endpoint.secret_data
}

output "grafana_metrics_endpoint_secret_name" {
  value = google_secret_manager_secret_version.grafana_metrics_endpoint.secret_data
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