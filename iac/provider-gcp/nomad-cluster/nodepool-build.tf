locals {
  build_pool_name     = "${var.prefix}orch-build"
  build_has_local_ssd = var.build_cluster_cache_disk_type == "local-ssd"
  build_startup_script = templatefile("${path.module}/scripts/start-client.sh", {
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
    NODE_POOL                    = var.build_node_pool
    BASE_HUGEPAGES_PERCENTAGE    = var.build_base_hugepages_percentage
    CACHE_DISK_COUNT             = var.build_cluster_cache_disk_count
    LOCAL_SSD                    = local.build_has_local_ssd ? "true" : "false"
  })
}

resource "google_compute_health_check" "build_nomad_check" {
  name                = "${local.build_pool_name}-nomad-build-cluster-check"
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

resource "google_compute_instance_group_manager" "build_pool" {
  name = "${local.build_pool_name}-ig"

  version {
    name              = google_compute_instance_template.build.id
    instance_template = google_compute_instance_template.build.id
  }

  # TODO: (2025-10-01) - keep this here for a migration period (at least until 2025-10-15)
  named_port {
    name = var.docker_reverse_proxy_port.name
    port = var.docker_reverse_proxy_port.port
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.build_nomad_check.id
    initial_delay_sec = 600
  }

  # Server is a stateful cluster, so the update strategy used to roll out a new GCE Instance Template must be
  # a rolling update.
  update_policy {
    type                    = var.environment == "dev" ? "PROACTIVE" : "OPPORTUNISTIC"
    minimal_action          = "REPLACE"
    max_surge_fixed         = 1
    max_surge_percent       = null
    max_unavailable_fixed   = 1
    max_unavailable_percent = null
    replacement_method      = "SUBSTITUTE"
  }

  base_instance_name = local.build_pool_name
  target_size        = var.build_cluster_size
  target_pools       = []

  depends_on = [
    google_compute_instance_template.build,
  ]
}

data "google_compute_image" "build_source_image" {
  family = var.build_image_family
}

resource "google_compute_instance_template" "build" {
  name_prefix = "${local.build_pool_name}-"

  instance_description = null
  machine_type         = var.build_machine_type
  min_cpu_platform     = var.min_cpu_platform

  labels = merge(
    var.labels,
    (var.environment != "dev" ? {
      goog-ops-agent-policy = "v2-x86-template-1-2-0-${var.gcp_zone}"
    } : {})
  )
  tags                    = [var.cluster_tag_name]
  metadata_startup_script = local.build_startup_script
  metadata = merge(
    {
      enable-osconfig         = "TRUE",
      enable-guest-attributes = "TRUE",
    },
  )

  scheduling {
    on_host_maintenance = "MIGRATE"
  }

  disk {
    boot         = true
    source_image = data.google_compute_image.build_source_image.id
    disk_size_gb = var.build_cluster_root_disk_size_gb
    disk_type    = var.build_boot_disk_type
  }


  # Cache disks - Local SSDs
  dynamic "disk" {
    for_each = [
      for _ in range(local.build_has_local_ssd ? var.build_cluster_cache_disk_count : 0) : {}
    ]
    content {
      auto_delete  = true
      boot         = false
      disk_size_gb = var.build_cluster_cache_disk_size_gb
      interface    = "NVME"
      disk_type    = var.build_cluster_cache_disk_type
      type         = "SCRATCH"
    }
  }

  # Cache Disk - Persistent Disk
  dynamic "disk" {
    for_each = [for n in range(!local.build_has_local_ssd ? 1 : 0) : {}]
    content {
      auto_delete  = true
      boot         = false
      type         = "PERSISTENT"
      disk_size_gb = var.build_cluster_cache_disk_size_gb
      disk_type    = var.build_cluster_cache_disk_type
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
      condition     = local.build_has_local_ssd || var.build_cluster_cache_disk_count == 1
      error_message = "When using persistent disks for the build cluster cache, only 1 disk is supported."
    }

    precondition {
      condition     = !local.build_has_local_ssd || var.build_cluster_cache_disk_size_gb == 375
      error_message = "When using local-ssd for the build cluster cache, each disk must be exactly 375 GB."
    }

    create_before_destroy = true
  }

  depends_on = [
    google_storage_bucket_object.setup_config_objects,
  ]
}
