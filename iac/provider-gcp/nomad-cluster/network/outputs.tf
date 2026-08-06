output "network_hardening_stage_ready" {
  description = "Exact administrative firewall identities used to order the guarded network stage."
  value = {
    iap_allow = try(google_compute_firewall.iap_remote_connection_firewall_ingress[0].id, "disabled")
    deny      = google_compute_firewall.remote_connection_firewall_ingress.id
    legacy    = google_compute_firewall.internal_remote_connection_firewall_ingress.id
  }
}
