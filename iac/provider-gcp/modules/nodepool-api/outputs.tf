output "instance_group" {
  description = "Self-link of the regional instance group, for use as a load balancer backend."
  value       = google_compute_region_instance_group_manager.pool.instance_group
}
