data "google_project" "current" {}

locals {
  grpc_api_http2_mtls_ca_location         = var.grpc_api_http2_mtls_ca_location != "" ? var.grpc_api_http2_mtls_ca_location : var.gcp_region
  grpc_api_http2_mtls_backend_server_name = var.grpc_api_http2_mtls_backend_server_name != "" ? var.grpc_api_http2_mtls_backend_server_name : "grpc-api.${var.domain_name}"
  grpc_api_http2_lb_client_certificate_id = "projects/${var.gcp_project_id}/locations/global/certificates/${var.prefix}grpc-api-http2-lb-client"

  grpc_api_http2_managed_ingress_tls = var.grpc_api_http2_mtls_managed_pki_enabled ? {
    certificate_consul_key     = "ingress/http2/grpc-api/tls/cert"
    private_key_consul_key     = "ingress/http2/grpc-api/tls/key"
    client_ca_consul_key       = "ingress/http2/grpc-api/tls/client-ca"
    reload_consul_key          = "ingress/http2/grpc-api/tls/reload"
    require_client_certificate = true
  } : null

  effective_ingress_http2_tls = var.ingress_http2_tls != null ? var.ingress_http2_tls : local.grpc_api_http2_managed_ingress_tls

  grpc_api_http2_managed_backend_tls = var.grpc_api_http2_mtls_managed_pki_enabled ? {
    server_name                = local.grpc_api_http2_mtls_backend_server_name
    trust_anchor_pems          = google_privateca_certificate_authority.grpc_api_http2[0].pem_ca_certificates
    intermediate_ca_pems       = []
    client_certificate         = terraform_data.grpc_api_http2_lb_client_certificate[0].output
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
    reload_consul_key      = coalesce(local.effective_ingress_http2_tls.reload_consul_key, "ingress/http2/grpc-api/tls/reload")
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

resource "terraform_data" "grpc_api_http2_lb_client_certificate" {
  count = var.grpc_api_http2_mtls_managed_pki_enabled ? 1 : 0

  input = local.grpc_api_http2_lb_client_certificate_id

  triggers_replace = {
    certificate_id       = local.grpc_api_http2_lb_client_certificate_id
    certificate_validity = var.grpc_api_http2_mtls_certificate_validity
    rotation             = time_rotating.grpc_api_http2_lb_client_certificate[0].id
  }

  provisioner "local-exec" {
    command = <<-EOF
      set -eu

      workdir="$(mktemp -d)"
      trap 'rm -rf "$${workdir}"' EXIT

      cert_id="${var.prefix}grpc-api-http2-lb-client-$(date -u +%Y%m%d%H%M%S)"

      gcloud privateca certificates create "$${cert_id}" \
        --project "${var.gcp_project_id}" \
        --issuer-pool "${google_privateca_ca_pool.grpc_api_http2[0].name}" \
        --issuer-location "${local.grpc_api_http2_mtls_ca_location}" \
        --generate-key \
        --key-output-file "$${workdir}/lb-client.key" \
        --cert-output-file "$${workdir}/lb-client.crt" \
        --dns-san "gcp-lb-client.${var.domain_name}" \
        --use-preset-profile "leaf_client_tls" \
        --validity "${var.grpc_api_http2_mtls_certificate_validity}" \
        --quiet

      if gcloud certificate-manager certificates describe "${var.prefix}grpc-api-http2-lb-client" \
        --project "${var.gcp_project_id}" \
        --location "global" >/dev/null 2>&1; then
        gcloud certificate-manager certificates update "${var.prefix}grpc-api-http2-lb-client" \
          --project "${var.gcp_project_id}" \
          --location "global" \
          --certificate-file "$${workdir}/lb-client.crt" \
          --private-key-file "$${workdir}/lb-client.key" \
          --quiet
      else
        gcloud certificate-manager certificates create "${var.prefix}grpc-api-http2-lb-client" \
          --project "${var.gcp_project_id}" \
          --location "global" \
          --scope "client-auth" \
          --certificate-file "$${workdir}/lb-client.crt" \
          --private-key-file "$${workdir}/lb-client.key" \
          --quiet
      fi
    EOF
  }

  depends_on = [
    google_privateca_certificate_authority.grpc_api_http2,
  ]
}
