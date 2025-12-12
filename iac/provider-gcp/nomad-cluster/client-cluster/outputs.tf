output "instance_group" {
  value       = google_compute_region_instance_group_manager.client_pool.instance_group
  description = "The client cluster instance group for load balancer backend"
}
