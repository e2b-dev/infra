output "cluster_name" {
  value = var.cluster_name
}

output "cluster_tag_name" {
  value = var.cluster_name
}

output "instance_group_id" {
  value = google_compute_instance_group_manager.api_cluster.id
}

output "instance_group_url" {
  value = google_compute_instance_group_manager.api_cluster.self_link
}

output "instance_group_name" {
  value = google_compute_instance_group_manager.api_cluster.name
}

output "instance_group" {
  value = google_compute_instance_group_manager.api_cluster.instance_group
}