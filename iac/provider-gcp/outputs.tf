output "api_controller_service_account_email" {
  description = "Email allowlist binding for the TAMS worker-capacity OIDC verifier."
  value       = module.init.api_controller_service_account_email
}

output "api_controller_service_account_unique_id" {
  description = "Immutable numeric subject allowlist binding for the TAMS worker-capacity OIDC verifier."
  value       = module.init.api_controller_service_account_unique_id
}
