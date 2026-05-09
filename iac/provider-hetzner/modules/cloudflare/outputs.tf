output "zone_id" {
  description = "Cloudflare zone ID for the root domain."
  value       = data.cloudflare_zone.domain.zone_id
}

output "zone_name" {
  description = "Cloudflare zone name."
  value       = data.cloudflare_zone.domain.name
}
