output "docker_reverse_proxy_service_account_key" {
  value = google_service_account_key.google_service_key.private_key
}
