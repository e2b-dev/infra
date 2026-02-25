# --- ACM Certificate ---
resource "aws_acm_certificate" "main" {
  domain_name = var.domain_name
  subject_alternative_names = concat(
    ["*.${var.domain_name}"],
    flatten([for d in var.additional_domains : [d, "*.${d}"]])
  )
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}cert"
  })
}

# --- Cloudflare DNS Zones ---
data "cloudflare_zone" "domain" {
  name = local.root_domain
}

data "cloudflare_zone" "domains_additional" {
  for_each = local.domain_map
  name     = each.value
}

# --- ACM DNS Validation Records (via Cloudflare) ---
locals {
  # Build a map of domain_validation_options keyed by domain name
  dvo_map = {
    for dvo in aws_acm_certificate.main.domain_validation_options : dvo.domain_name => {
      name  = dvo.resource_record_name
      type  = dvo.resource_record_type
      value = dvo.resource_record_value
    }
  }

  # Primary domain + wildcard share the same validation record
  primary_dvos = {
    for k, v in local.dvo_map : k => v
    if k == var.domain_name || k == "*.${var.domain_name}"
  }

  # Additional domain validation records
  additional_dvos = {
    for k, v in local.dvo_map : k => v
    if !contains([var.domain_name, "*.${var.domain_name}"], k)
  }
}

# Validation record for primary domain (covers domain + *.domain)
resource "cloudflare_record" "cert_validation_primary" {
  zone_id = data.cloudflare_zone.domain.id
  name    = local.dvo_map[var.domain_name].name
  value   = local.dvo_map[var.domain_name].value
  type    = local.dvo_map[var.domain_name].type
  ttl     = 60
}

# Validation records for additional domains
resource "cloudflare_record" "cert_validation_additional" {
  for_each = {
    for d_key, d_val in local.domain_map : d_key => d_val
    if contains(keys(local.dvo_map), d_val)
  }

  zone_id = data.cloudflare_zone.domains_additional[each.key].id
  name    = local.dvo_map[each.value].name
  value   = local.dvo_map[each.value].value
  type    = local.dvo_map[each.value].type
  ttl     = 60
}

# --- ACM Certificate Validation ---
resource "aws_acm_certificate_validation" "main" {
  certificate_arn = aws_acm_certificate.main.arn

  validation_record_fqdns = concat(
    [cloudflare_record.cert_validation_primary.hostname],
    [for r in cloudflare_record.cert_validation_additional : r.hostname]
  )
}

# --- Cloudflare DNS Records ---
# ALB records: api, docker subdomains
resource "cloudflare_record" "api" {
  zone_id = data.cloudflare_zone.domain.id
  name    = local.is_subdomain ? "api.${local.subdomain}" : "api"
  value   = aws_lb.alb.dns_name
  type    = "CNAME"
  ttl     = 1
  proxied = false
}

resource "cloudflare_record" "docker" {
  zone_id = data.cloudflare_zone.domain.id
  name    = local.is_subdomain ? "docker.${local.subdomain}" : "docker"
  value   = aws_lb.alb.dns_name
  type    = "CNAME"
  ttl     = 1
  proxied = false
}

# NLB wildcard record: *.domain -> NLB for WebSocket sessions
resource "cloudflare_record" "wildcard" {
  zone_id = data.cloudflare_zone.domain.id
  name    = local.is_subdomain ? "*.${local.subdomain}" : "*"
  value   = aws_lb.nlb.dns_name
  type    = "CNAME"
  ttl     = 1
  proxied = false
}

# Additional domains: wildcard -> NLB
resource "cloudflare_record" "wildcard_additional" {
  for_each = local.domain_map

  zone_id = data.cloudflare_zone.domains_additional[each.key].id
  name    = "*"
  value   = aws_lb.nlb.dns_name
  type    = "CNAME"
  ttl     = 1
  proxied = false
}

# Additional domains: api, docker -> ALB
resource "cloudflare_record" "api_additional" {
  for_each = local.domain_map

  zone_id = data.cloudflare_zone.domains_additional[each.key].id
  name    = "api"
  value   = aws_lb.alb.dns_name
  type    = "CNAME"
  ttl     = 1
  proxied = false
}

resource "cloudflare_record" "docker_additional" {
  for_each = local.domain_map

  zone_id = data.cloudflare_zone.domains_additional[each.key].id
  name    = "docker"
  value   = aws_lb.alb.dns_name
  type    = "CNAME"
  ttl     = 1
  proxied = false
}

