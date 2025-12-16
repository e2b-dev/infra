locals {
  client_pool_name     = "${var.prefix}${var.client_cluster_name}"
  client_has_local_ssd = var.client_cluster_cache_disk_type == "local-ssd"
  client_startup_script = templatefile("${path.module}/scripts/start-client.sh", {
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    SCRIPTS_BUCKET               = var.cluster_setup_bucket_name
    FC_KERNELS_BUCKET_NAME       = var.fc_kernels_bucket_name
    FC_VERSIONS_BUCKET_NAME      = var.fc_versions_bucket_name
    FC_ENV_PIPELINE_BUCKET_NAME  = var.fc_env_pipeline_bucket_name
    DOCKER_CONTEXTS_BUCKET_NAME  = var.docker_contexts_bucket_name
    GCP_REGION                   = var.gcp_region
    GOOGLE_SERVICE_ACCOUNT_KEY   = var.google_service_account_key
    NOMAD_TOKEN                  = var.nomad_acl_token_secret
    CONSUL_TOKEN                 = var.consul_acl_token_secret
    RUN_CONSUL_FILE_HASH         = local.file_hash["scripts/run-consul.sh"]
    RUN_NOMAD_FILE_HASH          = local.file_hash["scripts/run-nomad.sh"]
    CONSUL_GOSSIP_ENCRYPTION_KEY = google_secret_manager_secret_version.consul_gossip_encryption_key.secret_data
    CONSUL_DNS_REQUEST_TOKEN     = google_secret_manager_secret_version.consul_dns_request_token.secret_data
    NFS_IP_ADDRESS               = var.filestore_cache_enabled ? join(",", module.filestore[0].nfs_ip_addresses) : ""
    NFS_MOUNT_PATH               = local.nfs_mount_path
    NFS_MOUNT_SUBDIR             = local.nfs_mount_subdir
    NFS_MOUNT_OPTS               = local.nfs_mount_opts
    USE_FILESTORE_CACHE          = var.filestore_cache_enabled
    NODE_POOL                    = var.orchestrator_node_pool
    BASE_HUGEPAGES_PERCENTAGE    = var.orchestrator_base_hugepages_percentage
    CACHE_DISK_COUNT             = var.client_cluster_cache_disk_count
    LOCAL_SSD                    = local.client_has_local_ssd ? "true" : "false"
  })
}


resource "google_compute_health_check" "client_nomad_check" {
  name                = "${local.client_pool_name}-nomad-client-check"
  check_interval_sec  = 15
  timeout_sec         = 10
  healthy_threshold   = 2
  unhealthy_threshold = 10 # 50 seconds

  log_config {
    enable = true
  }

  http_health_check {
    request_path = "/v1/agent/health"
    port         = var.nomad_port
  }
}

resource "google_compute_region_autoscaler" "client" {
  count = var.client_cluster_size < var.client_cluster_size_max ? 1 : 0

  name   = "${local.client_pool_name}-client-autoscaler"
  region = var.gcp_region
  target = google_compute_region_instance_group_manager.client_pool.id

  autoscaling_policy {
    max_replicas    = var.client_cluster_size_max
    min_replicas    = var.client_cluster_size
    cooldown_period = 240
    # Turn off autoscaling when the cluster size is equal to the maximum size.
    mode = "ONLY_SCALE_OUT"

    cpu_utilization {
      target = var.client_cluster_autoscaling_cpu_target
    }

    dynamic "metric" {
      for_each = var.client_cluster_autoscaling_memory_target < 100 ? [1] : []
      content {
        name   = "agent.googleapis.com/memory/percent_used"
        type   = "GAUGE"
        filter = "resource.type = \"gce_instance\" AND metric.labels.state = \"used\""
        target = var.client_cluster_autoscaling_memory_target
      }
    }
  }
}

resource "google_compute_region_instance_group_manager" "client_pool" {
  name   = "${local.client_pool_name}-rig"
  region = var.gcp_region

  target_size = var.client_cluster_size < var.client_cluster_size_max ? null : var.client_cluster_size

  version {
    name              = google_compute_instance_template.client.id
    instance_template = google_compute_instance_template.client.id
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.client_nomad_check.id
    initial_delay_sec = 600
  }

  distribution_policy_target_shape = "BALANCED"

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

  base_instance_name = local.client_pool_name
  target_pools       = []

  depends_on = [
    google_compute_instance_template.client,
  ]
}

data "google_compute_image" "client_source_image" {
  family = var.client_image_family
}

resource "google_compute_instance_template" "client" {
  name_prefix = "${local.client_pool_name}-"

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
    auto_delete  = true
    boot         = true
    source_image = data.google_compute_image.client_source_image.id
    disk_size_gb = var.client_cluster_root_disk_size_gb
    disk_type    = var.client_boot_disk_type
  }

  # Cache disks - Local SSDs
  dynamic "disk" {
    for_each = [
      for _ in range(local.client_has_local_ssd ? var.client_cluster_cache_disk_count : 0) : {}
    ]

    content {
      auto_delete  = true
      boot         = false
      disk_size_gb = var.client_cluster_cache_disk_size_gb
      interface    = "NVME"
      disk_type    = var.client_cluster_cache_disk_type
      type         = "SCRATCH"
    }
  }

  # Cache Disk - Persistent Disk
  dynamic "disk" {
    for_each = [for n in range(!local.client_has_local_ssd ? 1 : 0) : {}]
    content {
      auto_delete  = true
      boot         = false
      type         = "PERSISTENT"
      disk_size_gb = var.client_cluster_cache_disk_size_gb
      disk_type    = var.client_cluster_cache_disk_type
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
    precondition {
      condition     = local.client_has_local_ssd || var.client_cluster_cache_disk_count == 1
      error_message = "When using persistent disks for the client cluster cache, only 1 disk is supported."
    }
    precondition {
      condition     = !local.client_has_local_ssd || var.client_cluster_cache_disk_size_gb == 375
      error_message = "When using local-ssd for the client cluster cache, each disk must be exactly 375 GB."
    }
    create_before_destroy = true
  }

  depends_on = [
    google_storage_bucket_object.setup_config_objects["scripts/run-nomad.sh"],
    google_storage_bucket_object.setup_config_objects["scripts/run-consul.sh"]
  ]
}
