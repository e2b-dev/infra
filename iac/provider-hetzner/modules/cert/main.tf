/**
 * Hetzner Cert Module — Let's Encrypt Wildcard via ACME-DNS-01
 *
 * Issues a Let's Encrypt wildcard certificate for *.{domain_name} using
 * the DNS-01 challenge fulfilled via Hetzner DNS API records.
 *
 * 1:1 Manus pattern (provider-aws domain.tf):
 *   - Wildcard cert *.{domain}
 *   - DNS-01 validation (no HTTP exposure required for issuance)
 *   - DNS records auto-cleaned by acme provider after validation
 *
 * EU-sovereign: Let's Encrypt is a non-profit (Internet Security Research
 * Group, US-based but operationally global, no data-residency concerns
 * for cert issuance metadata). Cert artefacts stay on Hetzner.
 *
 * The issued cert is registered as a Hetzner Cloud Certificate
 * (`hcloud_certificate` with type=uploaded) so the Hetzner Cloud Load
 * Balancer can use it for HTTPS termination at the edge.
 *
 * Auto-renewal: re-running `terraform apply` re-issues if days_until_renewal
 * is reached. For continuous renewal, schedule a periodic apply (e.g. 30d).
 */

terraform {
  required_providers {
    acme = {
      source  = "vancluever/acme"
      version = "~> 2.21"
    }
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.51.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}

# ─────────────────────────── ACME Account ───────────────────────────

resource "tls_private_key" "acme_account_key" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "acme_registration" "account" {
  account_key_pem = tls_private_key.acme_account_key.private_key_pem
  email_address   = var.acme_email
}

# ─────────────────────────── Wildcard Certificate Request ───────────────────────────

resource "acme_certificate" "wildcard" {
  account_key_pem = acme_registration.account.account_key_pem

  common_name = var.domain_name
  subject_alternative_names = concat(
    ["*.${var.domain_name}"],
    var.additional_sans,
  )

  min_days_remaining = var.min_days_remaining

  # DNS-01 challenge via Hetzner DNS provider.
  # The acme provider auto-creates and removes the _acme-challenge.{domain}
  # TXT records during validation.
  dns_challenge {
    provider = "hetzner"
    config = {
      HETZNER_API_KEY = var.hetzner_dns_token
    }
  }

  # Recursive nameserver for ACME DNS-01 propagation checks.
  # Hetzner DNS publishes via `helium.ns.hetzner.de` etc., but the acme
  # provider needs a public recursor to verify propagation.
  recursive_nameservers = var.recursive_nameservers

  must_staple = false
}

# ─────────────────────────── Hetzner Cloud Certificate (LB termination) ───────────────────────────
# Uploads the issued cert into the Hetzner Cloud certificate store so
# Hetzner Cloud Load Balancer can perform HTTPS termination natively.

resource "hcloud_certificate" "wildcard" {
  count = var.upload_to_hcloud ? 1 : 0

  name        = "${var.prefix}wildcard-${replace(var.domain_name, ".", "-")}"
  certificate = acme_certificate.wildcard.certificate_pem
  private_key = acme_certificate.wildcard.private_key_pem

  labels = merge(var.common_labels, {
    component = "cert"
    domain    = var.domain_name
    issuer    = "letsencrypt"
    cert_type = "wildcard"
  })
}
