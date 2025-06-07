output "cluster_name" {
  value = var.cluster_name
}

output "cluster_tag_name" {
  value = var.cluster_name
}

output "instance_group" {
  value = google_compute_instance_group_manager.client_cluster.instance_group
}

output "regional_instance_group" {
  value = google_compute_region_instance_group_manager.client_cluster.instance_group
}