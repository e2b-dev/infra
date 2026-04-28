locals {
  domain_parts        = split(".", var.domain_name)
  domain_is_subdomain = length(local.domain_parts) > 2

  // Take last 2 parts (1 dot)
  domain_root = local.domain_is_subdomain ? join(".", slice(local.domain_parts, length(local.domain_parts) - 2, length(local.domain_parts))) : var.domain_name

  use_cloudflare = var.dns_provider == "cloudflare"
  use_route53    = var.dns_provider == "route53"
}

// --- Cloudflare ---

data "cloudflare_zone" "domain" {
  count = local.use_cloudflare ? 1 : 0
  name  = local.domain_root
}

resource "cloudflare_record" "cert" {
  for_each = local.use_cloudflare ? {
    for dvo in aws_acm_certificate.wildcard.domain_validation_options : dvo.domain_name => {
      name  = dvo.resource_record_name
      value = dvo.resource_record_value
      type  = dvo.resource_record_type
    }
  } : {}

  zone_id = data.cloudflare_zone.domain[0].zone_id
  name    = each.value.name
  type    = each.value.type
  value   = each.value.value
  ttl     = 3600
}

resource "cloudflare_record" "routing" {
  count   = local.use_cloudflare ? 1 : 0
  zone_id = data.cloudflare_zone.domain[0].zone_id
  name    = "*.${var.domain_name}"
  type    = "CNAME"
  value   = aws_lb.ingress.dns_name
  ttl     = 3600
  proxied = false
}

// --- Route53 ---

data "aws_route53_zone" "domain" {
  count   = local.use_route53 ? 1 : 0
  zone_id = var.route53_zone_id
}

resource "aws_route53_record" "cert" {
  for_each = local.use_route53 ? {
    for dvo in aws_acm_certificate.wildcard.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  } : {}

  zone_id = data.aws_route53_zone.domain[0].zone_id
  name    = each.value.name
  type    = each.value.type
  ttl     = 300

  records = [each.value.record]
}

resource "aws_route53_record" "routing" {
  count   = local.use_route53 ? 1 : 0
  zone_id = data.aws_route53_zone.domain[0].zone_id
  name    = "*.${var.domain_name}"
  type    = "CNAME"
  ttl     = 300

  records = [aws_lb.ingress.dns_name]
}

// --- ACM Certificate (shared) ---

resource "aws_acm_certificate" "wildcard" {
  domain_name       = "*.${var.domain_name}"
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_acm_certificate_validation" "wildcard" {
  certificate_arn = aws_acm_certificate.wildcard.arn

  validation_record_fqdns = local.use_route53 ? [for record in aws_route53_record.cert : record.fqdn] : null
}
