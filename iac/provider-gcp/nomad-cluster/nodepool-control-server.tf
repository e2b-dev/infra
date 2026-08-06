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
    NOMAD_VOTER_HEALTH_SCRIPT    = file("${path.module}/scripts/nomad-voter-health.py")
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
    # The stock agent endpoint proves only that a leader is reachable. This
    # root-only sidecar proves that this exact GCE instance is an alive,
    # healthy voter and that the cluster still has quorum headroom.
    request_path = "/healthz"
    port         = 50001
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
  # Keep the quota-bounded dev canary in its selected zone; staging/prod retain
  # the provider's multi-zone regional distribution. The dev rollout itself
  # uses one surge and zero unavailable so zone shape cannot trade away quorum.
  distribution_policy_zones = (
    var.environment == "dev"
    ? [var.gcp_zone]
    : data.google_compute_zones.region_zones.names
  )

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
    type               = var.environment == "dev" ? "PROACTIVE" : "OPPORTUNISTIC"
    minimal_action     = "REPLACE"
    replacement_method = "SUBSTITUTE"

    // Keep PROACTIVE redistribution to maintain even server distribution across zones for Raft quorum resilience.
    // Note: redistributed instances will pick up the current instance template, which may apply pending template
    // changes as a side effect of zone rebalancing. This is an acceptable trade-off for server quorum safety.
    instance_redistribution_type = "PROACTIVE"
    // The dev control plane must bring one replacement server through the
    // health check and min_ready_sec window before removing an existing voter.
    // A zero-surge replacement can reduce a three-voter Raft cluster below
    // quorum while the new servers stabilize. One surge with zero unavailable
    // serializes the rollout around a fourth server instead. Non-dev retains
    // the upstream no-unavailability, one-per-zone surge.
    max_unavailable_fixed = 0
    max_surge_fixed = (
      var.environment == "dev"
      ? 1
      : length(data.google_compute_zones.region_zones.names)
    )

    // Wait 120s after instance is "healthy" before considering it truly ready
    // Gives Consul time to join Raft before GCP proceeds to kill old instances
    min_ready_sec = 120
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.server_nomad_check.id
    initial_delay_sec = 120
  }

  # The strict voter/quorum endpoint is an updater readiness gate, not an
  # auto-repair trigger. During quorum loss every remaining voter intentionally
  # reports unhealthy; allowing failed-health repair would then churn healthy
  # Raft members together. Preserve infrastructure-failure repair while making
  # application-health failure observational only.
  instance_lifecycle_policy {
    default_action_on_failure = "REPAIR"
    force_update_on_repair    = "NO"
    on_failed_health_check    = "DO_NOTHING"
  }

  lifecycle {
    create_before_destroy = false
  }

  depends_on = [
    google_compute_instance_template.server,
  ]
}

data "google_compute_image" "server_source_image" {
  family  = var.server_image_family
  project = var.gcp_project_id
}

resource "google_compute_instance_template" "server" {
  name_prefix = "${local.server_pool_name}-"

  instance_description = null
  machine_type         = var.server_machine_type

  tags                    = [var.cluster_tag_name]
  metadata_startup_script = local.server_startup_script
  metadata = merge({
    enable-osconfig         = "TRUE",
    enable-guest-attributes = "TRUE",
    cluster-size            = var.server_cluster_size,
  }, local.os_login_enabled.server ? { enable-oslogin = "TRUE" } : {})

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
    network = var.network_name

    # Invited-beta administration uses IAP/OS Login. When the shared NAT is
    # enabled, control servers do not receive directly reachable public IPs.
    dynamic "access_config" {
      for_each = var.api_use_nat ? [] : ["public_ip"]
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
    terraform_data.os_login_operator_access_guard,
    google_storage_bucket_object.setup_config_objects["scripts/run-nomad.sh"],
    google_storage_bucket_object.setup_config_objects["scripts/run-consul.sh"]
  ]
}
