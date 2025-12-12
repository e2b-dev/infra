moved {
  from = google_compute_region_instance_group_manager.client_pool
  to   = module.client_cluster["0"].google_compute_region_instance_group_manager.client_pool
}

moved {
  from = google_compute_instance_template.client
  to   = module.client_cluster["0"].google_compute_instance_template.client
}

moved {
  from = google_compute_health_check.client_nomad_check
  to   = module.client_cluster["0"].google_compute_health_check.client_nomad_check
}

moved {
  from = google_compute_region_autoscaler.client
  to   = module.client_cluster["0"].google_compute_region_autoscaler.client
}