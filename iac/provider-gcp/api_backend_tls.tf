resource "tls_private_key" "api_backend" {
  count = var.api_http2_backend_enabled ? 1 : 0

  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}

resource "tls_self_signed_cert" "api_backend" {
  count = var.api_http2_backend_enabled ? 1 : 0

  private_key_pem = tls_private_key.api_backend[0].private_key_pem

  subject {
    common_name  = "api.internal.${var.domain_name}"
    organization = "E2B"
  }

  dns_names = [
    "api.internal.${var.domain_name}",
  ]

  validity_period_hours = 24 * 365
  early_renewal_hours   = 24 * 30

  allowed_uses = [
    "key_encipherment",
    "digital_signature",
    "server_auth",
  ]
}
