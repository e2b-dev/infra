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

output "orchestration_repository_name" {
  value = google_artifact_registry_repository.orchestration_repository.name
}

output "cloudflare_api_token_secret_name" {
  value = google_secret_manager_secret.cloudflare_api_token.name
}

output "notification_email_secret_version" {
  value = google_secret_manager_secret_version.notification_email_value
}

output "vault_kms_keyring" {
  value       = google_kms_key_ring.vault.name
  description = "GCP KMS keyring name for Vault auto-unseal"
}

output "vault_kms_crypto_key" {
  value       = google_kms_crypto_key.vault_unseal.name
  description = "GCP KMS crypto key name for Vault auto-unseal"
}

output "vault_root_key_secret_id" {
  value       = google_secret_manager_secret.vault_root_key.secret_id
  description = "Secret Manager secret ID for Vault root key"
}

output "vault_recovery_keys_secret_id" {
  value       = google_secret_manager_secret.vault_recovery_keys.secret_id
  description = "Secret Manager secret ID for Vault recovery keys"
}

output "vault_api_approle_secret_id" {
  value       = google_secret_manager_secret.vault_api_approle.secret_id
  description = "Secret Manager secret ID for Vault API service AppRole credentials"
}

output "vault_orchestrator_approle_secret_id" {
  value       = google_secret_manager_secret.vault_orchestrator_approle.secret_id
  description = "Secret Manager secret ID for Vault Orchestrator service AppRole credentials"
}
