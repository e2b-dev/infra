output "network_hardening_stage_ready" {
  description = "Exact template and MIG identities used to order a guarded pool stage."
  value = {
    instance_template = google_compute_instance_template.template.id
    managed_group     = google_compute_region_instance_group_manager.pool.id
  }
}
