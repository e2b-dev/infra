output "logs_proxy_ip" {
  value = module.network.logs_proxy_ip
}

output "monitoring_keeper_count" {
  value = module.monitoring_cluster.keeper_count
}

output "monitoring_server_count" {
  value = module.monitoring_cluster.server_count
}

output "monitoring_keeper_service_port" {
  value = module.monitoring_cluster.keeper_service_port
}

output "monitoring_server_service_port" {
  value = module.monitoring_cluster.server_service_port
}
