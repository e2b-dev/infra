locals {
  use_isolated_nodes = var.isolated_client_cluster_target_size > 0

  isolated_client_pool_name = "${var.prefix}${var.client_cluster_name}-isolated"
}

resource "google_compute_node_template" "isolated-client" {
  count       = local.use_isolated_nodes ? 1 : 0
  name        = "${local.isolated_client_pool_name}-node-template"
  region      = var.gcp_region
  node_type   = var.client_node_type
  description = "Sole tenant node template for orchestrators"
}

resource "google_compute_node_group" "isolated-client" {
  count       = local.use_isolated_nodes ? 1 : 0
  name        = "${local.isolated_client_pool_name}-node-group"
  zone        = var.gcp_zone
  description = "Sole tenant node group for orchestrators"

  initial_size  = 1
  node_template = google_compute_node_template.isolated-client[0].id
}

resource "google_compute_instance_template" "isolated-client" {
  count = local.use_isolated_nodes ? 1 : 0

  name_prefix = "${local.isolated_client_pool_name}-"

  instance_description = null
  machine_type         = var.client_machine_type
  min_cpu_platform     = var.min_cpu_platform

  labels = merge(
    var.labels,
    (var.environment != "dev" ? {
      goog-ops-agent-policy = "v2-x86-template-1-2-0-${var.gcp_zone}"
    } : {})
  )
  tags                    = [var.cluster_tag_name]
  metadata_startup_script = local.client_startup_script
  metadata = {
    enable-osconfig         = "TRUE",
    enable-guest-attributes = "TRUE",
  }

  scheduling {
    on_host_maintenance = "MIGRATE"
  }

  disk {
    boot         = true
    source_image = data.google_compute_image.client_source_image.id
    disk_size_gb = 300
    disk_type    = "pd-ssd"
  }

  dynamic "disk" {
    for_each = [for n in range(var.client_cluster_cache_disk_count) : {}]

    content {
      auto_delete  = true
      boot         = false
      disk_size_gb = 375
      interface    = "NVME"
      disk_type    = "local-ssd"
      type         = "SCRATCH"
    }
  }

  network_interface {
    network = var.network_name

    dynamic "access_config" {
      for_each = ["public_ip"]
      content {}
    }
  }

  # For a full list of oAuth 2.0 Scopes, see https://developers.google.com/identity/protocols/googlescopes
  service_account {
    email = var.google_service_account_email
    scopes = [
      "userinfo-email",
      "compute-ro",
      "https://www.googleapis.com/auth/logging.write",
      "https://www.googleapis.com/auth/monitoring.write",
      "https://www.googleapis.com/auth/trace.append",
      "https://www.googleapis.com/auth/cloud-platform"
    ]
  }

  # Per Terraform Docs (https://www.terraform.io/docs/providers/google/r/compute_instance_template.html#using-with-instance-group-manager),
  # we need to create a new instance template before we can destroy the old one. Note that any Terraform resource on
  # which this Terraform resource depends will also need this lifecycle statement.
  lifecycle {
    create_before_destroy = true
  }

  depends_on = [
    google_storage_bucket_object.setup_config_objects["scripts/run-nomad.sh"],
    google_storage_bucket_object.setup_config_objects["scripts/run-consul.sh"]
  ]
}

resource "google_compute_region_instance_group_manager" "isolated-client-pool" {
  count = local.use_isolated_nodes ? 1 : 0

  name                      = "${local.isolated_client_pool_name}-rig"
  region                    = var.gcp_region
  distribution_policy_zones = [var.gcp_zone]

  target_size = var.isolated_client_cluster_target_size

  version {
    name              = google_compute_instance_template.isolated-client[0].id
    instance_template = google_compute_instance_template.isolated-client[0].id
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.client_nomad_check.id
    initial_delay_sec = 600
  }

  distribution_policy_target_shape = "EVEN"

  # Server is a stateful cluster, so the update strategy used to roll out a new GCE Instance Template must be
  # a rolling update.
  update_policy {
    type                         = var.environment == "dev" ? "PROACTIVE" : "OPPORTUNISTIC"
    minimal_action               = "REPLACE"
    max_surge_fixed              = 10
    max_surge_percent            = null
    max_unavailable_fixed        = 5
    max_unavailable_percent      = null
    replacement_method           = "SUBSTITUTE"
    instance_redistribution_type = "NONE"
  }

  base_instance_name = local.isolated_client_pool_name
  target_pools       = []
}
