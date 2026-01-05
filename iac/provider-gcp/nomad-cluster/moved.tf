moved {
  from = google_compute_region_instance_group_manager.client_pool
  to   = module.client_cluster["default"].google_compute_region_instance_group_manager.pool
}

moved {
  from = google_compute_instance_template.client
  to   = module.client_cluster["default"].google_compute_instance_template.template
}

moved {
  from = google_compute_region_autoscaler.client
  to   = module.client_cluster["default"].google_compute_region_autoscaler.autoscaler
}

moved {
  from = google_compute_health_check.client_nomad_check
  to   = module.client_cluster["default"].google_compute_health_check.nomad_check
}
