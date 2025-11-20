output "regional_lb_ip_address" {
  description = "IP address of the regional external load balancer"
  value       = google_compute_address.regional_management_lb.address
}

output "regional_lb_certificate_pem" {
  description = "PEM-encoded self-signed certificate for the regional load balancer"
  value       = tls_self_signed_cert.regional_management_lb.cert_pem
  sensitive   = true
}

output "regional_lb_private_key_pem" {
  description = "PEM-encoded private key for the regional load balancer certificate"
  value       = tls_private_key.regional_management_lb.private_key_pem
  sensitive   = true
}
