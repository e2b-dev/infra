output "server_ids" {
  value = [for s in hcloud_server.clickhouse : s.id]
}

output "server_private_ips" {
  value = [for s in hcloud_server.clickhouse : tolist(s.network)[0].ip]
}

output "server_names" {
  value = [for s in hcloud_server.clickhouse : s.name]
}

output "data_volume_ids" {
  value = [for v in hcloud_volume.clickhouse_data : v.id]
}
