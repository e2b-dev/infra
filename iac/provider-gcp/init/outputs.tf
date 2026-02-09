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

output "launch_darkly_api_key_secret_version" {
  value = google_secret_manager_secret_version.launch_darkly_api_key
}

output "analytics_collector_host_secret_name" {
  value = google_secret_manager_secret.analytics_collector_host.name
}

output "analytics_collector_api_token_secret_name" {
  value = google_secret_manager_secret.analytics_collector_api_token.name
}

output "routing_domains_secret_name" {
  value = google_secret_manager_secret.routing_domains.name
}

output "orchestration_repository_name" {
  value = google_artifact_registry_repository.orchestration_repository.name
}

output "cloudflare_api_token_secret_name" {
  value = google_secret_manager_secret.cloudflare_api_token.name
}

output "notification_email_secret_version" {
  value = google_secret_manager_secret_version.notification_email_value
}

output "redis_cluster_url_secret_version" {
  value = google_secret_manager_secret_version.redis_cluster_url
}

output "redis_tls_ca_base64_secret_version" {
  value = google_secret_manager_secret_version.redis_tls_ca_base64
}

output "posthog_api_key_secret_name" {
  value = google_secret_manager_secret_version.posthog_api_key.secret
}

output "supabase_jwt_secret_name" {
  value = google_secret_manager_secret_version.supabase_jwt_secrets.secret
}

output "postgres_connection_string_secret_name" {
  value = google_secret_manager_secret.postgres_connection_string.name
}

output "loki_bucket_name" {
  value = google_storage_bucket.loki_storage_bucket.name
}

output "envs_docker_context_bucket_name" {
  value = google_storage_bucket.envs_docker_context.name
}

output "cluster_setup_bucket_name" {
  value = google_storage_bucket.setup_bucket.name
}

output "fc_env_pipeline_bucket_name" {
  description = "The name of the bucket to store the files for firecracker environment pipeline"
  value       = google_storage_bucket.fc_env_pipeline_bucket.name
}

output "fc_kernels_bucket_name" {
  value = google_storage_bucket.fc_kernels_bucket.name
}

output "fc_versions_bucket_name" {
  value = google_storage_bucket.fc_versions_bucket.name
}

output "fc_template_bucket_name" {
  value = google_storage_bucket.fc_template_bucket.name
}

output "fc_build_cache_bucket_name" {
  value = google_storage_bucket.fc_build_cache_bucket.name
}

output "clickhouse_backups_bucket_name" {
  value = google_storage_bucket.clickhouse_backups_bucket.name
}