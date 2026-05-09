/**
 * Hetzner DNS Module — Outputs
 */

output "zone_id" {
  description = "Hetzner DNS zone ID for the root domain."
  value       = data.hetznerdns_zone.main.id
}

output "zone_name" {
  description = "Hetzner DNS zone name."
  value       = data.hetznerdns_zone.main.name
}

output "zone_ns" {
  description = "Hetzner DNS authoritative nameservers (configure these at the registrar)."
  value       = data.hetznerdns_zone.main.ns
}

output "wildcard_record" {
  description = "The wildcard *.{domain} record (CNAME or A depending on lb_hostname)."
  value = (
    length(hetznerdns_record.wildcard_cname) > 0 ?
    hetznerdns_record.wildcard_cname[0].name :
    (length(hetznerdns_record.wildcard_a) > 0 ? hetznerdns_record.wildcard_a[0].name : null)
  )
}

output "manus_pattern_records" {
  description = "Map of Manus-pattern subdomain → record id."
  value = merge(
    {
      for k, r in hetznerdns_record.manus_pattern_subdomains :
      k => r.id
    },
    {
      for k, r in hetznerdns_record.manus_pattern_subdomains_a :
      k => r.id
    }
  )
}

output "vps_wildcard_record" {
  description = "VPS-pattern wildcard *.vm.{domain} record id."
  value       = try(hetznerdns_record.vps_wildcard[0].id, null)
}
