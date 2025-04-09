output "keeper_count" {
  value = var.keeper_cluster_size
}

output "server_count" {
  value = var.server_cluster_size
}

output "keeper_service_port" {
  value = var.keeper_service_port
}

output "server_service_port" {
  value = var.server_service_port
}

output "server_service_health_port" {
  value = var.server_service_health_port
}
