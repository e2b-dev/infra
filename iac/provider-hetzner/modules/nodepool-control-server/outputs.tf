output "server_ids" {
  value = [for s in hcloud_server.control_server : s.id]
}

output "server_private_ips" {
  value = [for s in hcloud_server.control_server : tolist(s.network)[0].ip]
}

output "server_names" {
  value = [for s in hcloud_server.control_server : s.name]
}

output "consul_servers_join_addr" {
  description = "Comma-separated private IPs for Consul retry-join."
  value       = join(",", [for s in hcloud_server.control_server : tolist(s.network)[0].ip])
}

output "nomad_servers_join_addr" {
  description = "Comma-separated private IPs for Nomad retry-join."
  value       = join(",", [for s in hcloud_server.control_server : tolist(s.network)[0].ip])
}
