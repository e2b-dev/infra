output "server_ids" {
  description = "Hetzner Cloud Server IDs in this pool."
  value       = [for s in hcloud_server.api : s.id]
}

output "server_ipv4_addresses" {
  description = "Public IPv4 addresses of pool servers."
  value       = [for s in hcloud_server.api : s.ipv4_address]
}

output "server_private_ips" {
  description = "Private (Cloud Network) IPs of pool servers."
  value       = [for s in hcloud_server.api : tolist(s.network)[0].ip]
}

output "server_names" {
  description = "Server names for Consul/Nomad node identification."
  value       = [for s in hcloud_server.api : s.name]
}
