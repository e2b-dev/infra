/**
 * Cloudflare Compatibility Module (legacy DNS migration path)
 *
 * Used during Cloudflare → Hetzner DNS migration. Keep records in Cloudflare
 * while Hetzner-DNS zones are seeded. Once migration is complete, set
 * use_cloudflare_dns = false in the parent module to disable this.
 *
 * Mirrors provider-aws/modules/cloudflare/.
 */

terraform {
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.52.5"
    }
  }
}

data "cloudflare_zone" "domain" {
  name = var.domain_root
}
