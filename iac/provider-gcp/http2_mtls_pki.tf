data "google_project" "current" {}

locals {
  grpc_api_http2_mtls_ca_location         = var.grpc_api_http2_mtls_ca_location != "" ? var.grpc_api_http2_mtls_ca_location : var.gcp_region
  grpc_api_http2_mtls_backend_server_name = var.grpc_api_http2_mtls_backend_server_name != "" ? var.grpc_api_http2_mtls_backend_server_name : "grpc-api.${var.domain_name}"

  grpc_api_http2_managed_ingress_tls = var.grpc_api_http2_mtls_managed_pki_enabled ? {
    certificate_consul_key     = "ingress/http2/grpc-api/tls/cert"
    private_key_consul_key     = "ingress/http2/grpc-api/tls/key"
    client_ca_consul_key       = "ingress/http2/grpc-api/tls/client-ca"
    require_client_certificate = true
  } : null

  effective_ingress_http2_tls = var.ingress_http2_tls != null ? var.ingress_http2_tls : local.grpc_api_http2_managed_ingress_tls

  grpc_api_http2_managed_backend_tls = var.grpc_api_http2_mtls_managed_pki_enabled ? {
    server_name                = local.grpc_api_http2_mtls_backend_server_name
    trust_anchor_pems          = [google_privateca_certificate_authority.grpc_api_http2[0].pem_ca_certificate]
    intermediate_ca_pems       = []
    client_certificate         = google_certificate_manager_certificate.grpc_api_http2_lb_client[0].id
    require_client_certificate = true
  } : null

  effective_grpc_api_http2_backend_tls = var.grpc_api_http2_backend_tls != null ? var.grpc_api_http2_backend_tls : local.grpc_api_http2_managed_backend_tls

  grpc_api_http2_cert_renewer = var.grpc_api_http2_mtls_managed_pki_enabled ? {
    gcp_project_id         = var.gcp_project_id
    ca_pool                = google_privateca_ca_pool.grpc_api_http2[0].name
    ca_id                  = google_privateca_certificate_authority.grpc_api_http2[0].certificate_authority_id
    ca_location            = local.grpc_api_http2_mtls_ca_location
    server_name            = local.grpc_api_http2_mtls_backend_server_name
    cert_validity          = var.grpc_api_http2_mtls_certificate_validity
    renew_interval         = tostring(var.grpc_api_http2_mtls_renew_interval_seconds)
    certificate_consul_key = local.effective_ingress_http2_tls.certificate_consul_key
    private_key_consul_key = local.effective_ingress_http2_tls.private_key_consul_key
    client_ca_consul_key   = local.effective_ingress_http2_tls.client_ca_consul_key
  } : null
}

resource "google_privateca_ca_pool" "grpc_api_http2" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  name     = "${var.prefix}grpc-api-http2"
  location = local.grpc_api_http2_mtls_ca_location
  tier     = "DEVOPS"

  issuance_policy {
    maximum_lifetime = "2592000s"

    allowed_issuance_modes {
      allow_config_based_issuance = true
      allow_csr_based_issuance    = true
    }

    baseline_values {
      ca_options {
        is_ca = false
      }

      key_usage {
        base_key_usage {
          digital_signature = true
          key_encipherment  = true
        }

        extended_key_usage {
          client_auth = true
          server_auth = true
        }
      }
    }
  }
}

resource "google_privateca_certificate_authority" "grpc_api_http2" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  pool                     = google_privateca_ca_pool.grpc_api_http2[0].name
  certificate_authority_id = "${var.prefix}grpc-api-http2"
  location                 = local.grpc_api_http2_mtls_ca_location
  deletion_protection      = true
  desired_state            = "ENABLED"
  lifetime                 = "315360000s"

  config {
    subject_config {
      subject {
        organization = "E2B"
        common_name  = "${var.prefix}grpc-api-http2-ca"
      }
    }

    x509_config {
      ca_options {
        is_ca = true
      }

      key_usage {
        base_key_usage {
          cert_sign = true
          crl_sign  = true
        }

        extended_key_usage {
          client_auth = false
          server_auth = false
        }
      }
    }
  }

  key_spec {
    algorithm = "RSA_PKCS1_4096_SHA256"
  }
}

resource "google_privateca_ca_pool_iam_member" "grpc_api_http2_instances" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  ca_pool = google_privateca_ca_pool.grpc_api_http2[0].id
  role    = "roles/privateca.certificateRequester"
  member  = "serviceAccount:${module.init.service_account_email}"
}

resource "google_privateca_ca_pool_iam_member" "grpc_api_http2_instances_auditor" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  ca_pool = google_privateca_ca_pool.grpc_api_http2[0].id
  role    = "roles/privateca.auditor"
  member  = "serviceAccount:${module.init.service_account_email}"
}

resource "time_rotating" "grpc_api_http2_lb_client_certificate" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  rotation_days = 20
}

resource "tls_private_key" "grpc_api_http2_lb_client" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  algorithm = "RSA"
  rsa_bits  = 2048

  lifecycle {
    replace_triggered_by = [time_rotating.grpc_api_http2_lb_client_certificate[0]]
  }
}

resource "tls_cert_request" "grpc_api_http2_lb_client" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  private_key_pem = tls_private_key.grpc_api_http2_lb_client[0].private_key_pem
  dns_names       = ["gcp-lb-client.${var.domain_name}"]

  subject {
    organization = "E2B"
    common_name  = "gcp-lb-client.${var.domain_name}"
  }
}

resource "google_privateca_certificate" "grpc_api_http2_lb_client" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  name                  = "${var.prefix}grpc-api-http2-lb-client"
  pool                  = google_privateca_ca_pool.grpc_api_http2[0].name
  certificate_authority = google_privateca_certificate_authority.grpc_api_http2[0].certificate_authority_id
  location              = local.grpc_api_http2_mtls_ca_location
  lifetime              = "2592000s"
  pem_csr               = tls_cert_request.grpc_api_http2_lb_client[0].cert_request_pem

  lifecycle {
    replace_triggered_by = [tls_cert_request.grpc_api_http2_lb_client[0]]
  }
}

resource "google_certificate_manager_certificate" "grpc_api_http2_lb_client" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  name     = "${var.prefix}grpc-api-http2-lb-client"
  location = "global"
  scope    = "CLIENT_AUTH"

  self_managed {
    pem_certificate = join("\n", google_privateca_certificate.grpc_api_http2_lb_client[0].pem_certificate_chain)
    pem_private_key = tls_private_key.grpc_api_http2_lb_client[0].private_key_pem
  }
}
