locals {
  server_pool_name = "${var.prefix}${var.server_cluster_name}"
  server_startup_script = templatefile("${path.module}/scripts/start-server.sh", {
    NUM_SERVERS                  = var.server_cluster_size
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    SCRIPTS_BUCKET               = var.cluster_setup_bucket_name
    NOMAD_TOKEN                  = var.nomad_acl_token_secret
    CONSUL_TOKEN                 = var.consul_acl_token_secret
    RUN_CONSUL_FILE_HASH         = local.file_hash["scripts/run-consul.sh"]
    RUN_NOMAD_FILE_HASH          = local.file_hash["scripts/run-nomad.sh"]
    CONSUL_GOSSIP_ENCRYPTION_KEY = google_secret_manager_secret_version.consul_gossip_encryption_key.secret_data
  })
}

resource "google_compute_health_check" "server_nomad_check" {
  name                = "${local.server_pool_name}-nomad-check"
  check_interval_sec  = 5
  timeout_sec         = 5
  healthy_threshold   = 2
  unhealthy_threshold = 10 # 50 seconds

  http_health_check {
    request_path = "/v1/agent/health"
    port         = var.nomad_port
  }
}

data "google_compute_zones" "region_zones" {
  region = var.gcp_region
}

resource "google_compute_region_instance_group_manager" "server_pool" {
  provider = google-beta

  region             = var.gcp_region
  name               = "${local.server_pool_name}-rig"
  base_instance_name = local.server_pool_name

  target_pools                     = []
  target_size                      = var.server_cluster_size
  distribution_policy_target_shape = "EVEN"

  version {
    instance_template = google_compute_instance_template.server.id
  }

  named_port {
    name = "nomad"
    port = var.nomad_port
  }

  # Server is a stateful cluster. In non-dev environments, use OPPORTUNISTIC updates so instance template
  # changes are only applied when instances are recreated for other reasons (e.g., auto-healing).
  # Proactive rolling replacements of servers can cause missed client heartbeats and secret revocations:
  # https://github.com/hashicorp/nomad/issues/9390
  update_policy {
    type           = var.environment == "dev" ? "PROACTIVE" : "OPPORTUNISTIC"
    minimal_action = "REPLACE"

    // Keep PROACTIVE redistribution to maintain even server distribution across zones for Raft quorum resilience.
    // Note: redistributed instances will pick up the current instance template, which may apply pending template
    // changes as a side effect of zone rebalancing. This is an acceptable trade-off for server quorum safety.
    instance_redistribution_type = "PROACTIVE"
    max_unavailable_fixed        = 0

    // The number has to be a multiple of the number of zones in the region
    max_surge_fixed = length(data.google_compute_zones.region_zones.names)

    // Wait 120s after instance is "healthy" before considering it truly ready
    // Gives Consul time to join Raft before GCP proceeds to kill old instances
    min_ready_sec = 120
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.server_nomad_check.id
    initial_delay_sec = 120
  }

  lifecycle {
    create_before_destroy = false
  }

  depends_on = [
    google_compute_instance_template.server,
  ]
}

data "google_compute_image" "server_source_image" {
  family = var.server_image_family
}

resource "google_compute_instance_template" "server" {
  name_prefix = "${local.server_pool_name}-"

  instance_description = null
  machine_type         = var.server_machine_type

  tags                    = local.server_network_tags
  metadata_startup_script = local.server_startup_script
  metadata = {
    enable-osconfig         = "TRUE",
    enable-guest-attributes = "TRUE",
    cluster-size            = var.server_cluster_size,
  }

  labels = merge(
    var.labels,
    (var.environment != "dev" ? {
      goog-ops-agent-policy = "v2-x86-template-1-2-0-${var.gcp_zone}"
    } : {})
  )
  scheduling {
    on_host_maintenance = "MIGRATE"
  }

  disk {
    boot         = true
    source_image = data.google_compute_image.server_source_image.self_link
    disk_size_gb = var.server_boot_disk_size_gb
    disk_type    = var.server_boot_disk_type
  }

  network_interface {
    network    = var.network_name
    subnetwork = local.server_subnetwork_name

    # Create access config dynamically. If a public ip is requested, we just need the empty `access_config` block
    # to automatically assign an external IP address.
    dynamic "access_config" {
      for_each = ["public_ip"]
      content {}
    }
  }

  service_account {
    email = var.google_service_account_email
    scopes = [
      "userinfo-email",
      "compute-ro",
      "https://www.googleapis.com/auth/monitoring.write",
      "https://www.googleapis.com/auth/logging.write",
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
