output "cluster_label" {
  value = local.cluster_label
}

output "server_ids" {
  value = module.worker_pool.server_ids
}

output "server_private_ips" {
  value = module.worker_pool.server_private_ips
}

output "server_names" {
  value = module.worker_pool.server_names
}
