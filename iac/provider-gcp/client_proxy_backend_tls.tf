locals {
  client_proxy_backend_tls_hostname = "client-proxy.internal.${var.domain_name}"
  client_proxy_backend_tls_enabled  = var.client_proxy_http2_backend_enabled && var.internal_tls
}

resource "tls_private_key" "client_proxy_backend" {
  count = local.client_proxy_backend_tls_enabled ? 1 : 0

  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}

resource "tls_cert_request" "client_proxy_backend" {
  count = local.client_proxy_backend_tls_enabled ? 1 : 0

  private_key_pem = tls_private_key.client_proxy_backend[0].private_key_pem

  subject {
    common_name  = local.client_proxy_backend_tls_hostname
    organization = "E2B"
  }

  dns_names = [
    local.client_proxy_backend_tls_hostname,
  ]
}

resource "google_privateca_ca_pool" "client_proxy_backend" {
  count = local.client_proxy_backend_tls_enabled ? 1 : 0

  name     = "${var.prefix}client-proxy-backend"
  location = var.gcp_region
  tier     = "ENTERPRISE"

  publishing_options {
    publish_ca_cert = true
    publish_crl     = true
  }

  labels = var.labels

  depends_on = [module.init]
}

resource "google_privateca_certificate_authority" "client_proxy_backend" {
  count = local.client_proxy_backend_tls_enabled ? 1 : 0

  certificate_authority_id = "${var.prefix}client-proxy-backend-ca"
  location                 = var.gcp_region
  pool                     = google_privateca_ca_pool.client_proxy_backend[0].name
  type                     = "SELF_SIGNED"
  desired_state            = "ENABLED"
  lifetime                 = "315360000s"

  deletion_protection                    = var.environment != "dev"
  skip_grace_period                      = var.environment == "dev"
  ignore_active_certificates_on_deletion = var.environment == "dev"

  config {
    subject_config {
      subject {
        common_name  = "E2B Client Proxy Backend Root CA"
        organization = "E2B"
      }
    }

    x509_config {
      ca_options {
        is_ca                  = true
        max_issuer_path_length = 0
      }

      key_usage {
        base_key_usage {
          cert_sign = true
          crl_sign  = true
        }

        extended_key_usage {
          server_auth = true
        }
      }
    }
  }

  key_spec {
    algorithm = "EC_P256_SHA256"
  }

  labels = var.labels
}

resource "google_privateca_certificate" "client_proxy_backend" {
  count = local.client_proxy_backend_tls_enabled ? 1 : 0

  name                  = "${var.prefix}client-proxy-backend"
  location              = var.gcp_region
  pool                  = google_privateca_ca_pool.client_proxy_backend[0].name
  certificate_authority = google_privateca_certificate_authority.client_proxy_backend[0].certificate_authority_id
  lifetime              = "7776000s"
  pem_csr               = tls_cert_request.client_proxy_backend[0].cert_request_pem

  labels = var.labels
}

resource "google_certificate_manager_trust_config" "client_proxy_backend" {
  count = local.client_proxy_backend_tls_enabled ? 1 : 0

  name        = "${var.prefix}client-proxy-backend"
  description = "Trust anchors for client-proxy backend TLS."
  location    = "global"

  trust_stores {
    trust_anchors {
      pem_certificate = google_privateca_certificate_authority.client_proxy_backend[0].pem_ca_certificates[0]
    }
  }

  labels = var.labels
}

resource "google_network_security_backend_authentication_config" "client_proxy_backend" {
  count = local.client_proxy_backend_tls_enabled ? 1 : 0

  name             = "${var.prefix}client-proxy-backend"
  description      = "Authenticates client-proxy backend certificates for the external load balancer."
  location         = "global"
  trust_config     = google_certificate_manager_trust_config.client_proxy_backend[0].id
  well_known_roots = "NONE"

  labels = var.labels
}
