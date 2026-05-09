output "server_ids" {
  value = [for s in hcloud_server.client : s.id]
}

output "server_private_ips" {
  value = [for s in hcloud_server.client : tolist(s.network)[0].ip]
}

output "server_names" {
  value = [for s in hcloud_server.client : s.name]
}
