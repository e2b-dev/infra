output "cluster_tag_name" {
  value = local.cluster_tag_name
}

output "cluster_tag_value" {
  value = local.cluster_tag_value
}

output "control_server" {
  value = {
    server_ids               = module.control_server.server_ids
    server_private_ips       = module.control_server.server_private_ips
    consul_servers_join_addr = module.control_server.consul_servers_join_addr
    nomad_servers_join_addr  = module.control_server.nomad_servers_join_addr
  }
}

output "api" {
  value = {
    server_ids         = module.api.server_ids
    server_private_ips = module.api.server_private_ips
  }
}

output "clickhouse" {
  value = {
    server_ids         = module.clickhouse.server_ids
    server_private_ips = module.clickhouse.server_private_ips
  }
}

output "client" {
  value = {
    server_ids         = module.client.server_ids
    server_private_ips = module.client.server_private_ips
  }
}

output "setup_files_hash" {
  description = "Hash of run-consul.sh + run-nomad.sh for cache-busting in cloud-init."
  value       = local.setup_files_hash
}
