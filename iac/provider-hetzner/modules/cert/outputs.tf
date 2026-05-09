/**
 * Hetzner Cert Module — Outputs
 */

output "certificate_pem" {
  description = "PEM-encoded certificate (chain included)."
  value       = acme_certificate.wildcard.certificate_pem
  sensitive   = true
}

output "issuer_pem" {
  description = "PEM-encoded issuer chain (intermediate)."
  value       = acme_certificate.wildcard.issuer_pem
  sensitive   = true
}

output "private_key_pem" {
  description = "PEM-encoded private key."
  value       = acme_certificate.wildcard.private_key_pem
  sensitive   = true
}

output "certificate_url" {
  description = "ACME certificate URL (revocation/renewal handle)."
  value       = acme_certificate.wildcard.certificate_url
}

output "common_name" {
  description = "Cert common name."
  value       = acme_certificate.wildcard.common_name
}

output "subject_alternative_names" {
  description = "Cert subject alternative names."
  value       = acme_certificate.wildcard.subject_alternative_names
}

output "hcloud_certificate_id" {
  description = "Hetzner Cloud Certificate ID (when upload_to_hcloud=true). null otherwise."
  value       = try(hcloud_certificate.wildcard[0].id, null)
}

output "hcloud_certificate_name" {
  description = "Hetzner Cloud Certificate name."
  value       = try(hcloud_certificate.wildcard[0].name, null)
}

output "not_after" {
  description = "Certificate expiry (ISO8601)."
  value       = acme_certificate.wildcard.certificate_not_after
}
