output "logs_proxy_ip" {
  value = google_compute_global_address.orch_logs_ip.address
}
