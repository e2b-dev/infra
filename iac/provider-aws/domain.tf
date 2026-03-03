locals {
  domain_parts        = split(".", var.domain_name)
  domain_is_subdomain = length(local.domain_parts) > 2

  // Take last 2 parts (1 dot)
  domain_root = local.domain_is_subdomain ? join(".", slice(local.domain_parts, length(local.domain_parts) - 2, length(local.domain_parts))) : var.domain_name
}

data "cloudflare_zone" "domain" {
  name = local.domain_root
}

resource "aws_acm_certificate" "wildcard" {
  domain_name       = "*.${var.domain_name}"
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_acm_certificate_validation" "wildcard" {
  certificate_arn = aws_acm_certificate.wildcard.arn
}

resource "cloudflare_record" "cert" {
  for_each = {
    for dvo in aws_acm_certificate.wildcard.domain_validation_options : dvo.domain_name => {
      name  = dvo.resource_record_name
      value = dvo.resource_record_value
      type  = dvo.resource_record_type
    }
  }

  zone_id = data.cloudflare_zone.domain.zone_id
  name    = each.value.name
  type    = each.value.type
  value   = each.value.value
  ttl     = 3600
}

resource "cloudflare_record" "routing" {
  zone_id = data.cloudflare_zone.domain.zone_id
  name    = "*.${var.domain_name}"
  type    = "CNAME"
  value   = aws_lb.ingress.dns_name
  ttl     = 3600
  proxied = false
}
