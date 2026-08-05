mock_provider "google" {
  mock_data "google_compute_image" {
    defaults = {
      id = "projects/monad-code/global/images/e2b-orch-fixture"
    }
  }
}

variables {
  cluster_size                             = 2
  machine_type                             = "n1-standard-8"
  min_cpu_platform                         = "Intel Skylake"
  boot_disk                                = { disk_type = "pd-ssd", size_gb = 100, swap_size_gb = 32 }
  network_interface_type                   = null
  cache_disks                              = { disk_type = "local-ssd", size_gb = 375, count = 1 }
  cluster_name                             = "monad-orch-client"
  image_family                             = "monad-e2b-runtime"
  gcp_project_id                           = "monad-code"
  gcp_region                               = "us-east4"
  gcp_zone                                 = "us-east4-c"
  network_name                             = "monad-vpc"
  use_cloud_nat                            = true
  cluster_tag_name                         = "monad-cluster"
  google_service_account_email             = "worker@monad-code.iam.gserviceaccount.com"
  nomad_port                               = 4646
  nomad_acl_token_secret                   = "test-nomad"
  consul_acl_token_secret                  = "test-consul"
  consul_gossip_encryption_key_secret_data = "test-gossip"
  consul_dns_request_token_secret_data     = "test-dns"
  node_pool                                = "default"
  docker_contexts_bucket_name              = "docker-contexts"
  cluster_setup_bucket_name                = "cluster-setup"
  fc_env_pipeline_bucket_name              = "fc-env-pipeline"
  fc_kernels_bucket_name                   = "fc-kernels"
  fc_versions_bucket_name                  = "fc-versions"
  fc_busybox_bucket_name                   = "fc-busybox"
  filestore_cache_enabled                  = false
  nfs_ip_addresses                         = []
  nfs_mount_path                           = "/orchestrator/shared-store"
  nfs_mount_subdir                         = "chunks-cache"
  nfs_mount_opts                           = ""
  base_hugepages_percentage                = 80
  enable_os_login                          = false
  os_login_operator_access_confirmed       = true
  environment                              = "dev"
  labels                                   = {}
  file_hash = {
    "scripts/configure-docker-gcp.sh" = "abcde"
    "scripts/run-consul.sh"           = "abcde"
    "scripts/run-nomad.sh"            = "abcde"
  }
  set_orchestrator_version_metadata  = true
  persistent_volume_types            = {}
  workload_autoscaler_shadow_enabled = true
}

run "shadow_keeps_terraform_floor" {
  command = plan

  variables {
    autoscaler = { size_max = 2, cpu_target = 1, memory_target = 100 }
  }

  assert {
    condition     = google_compute_region_instance_group_manager.pool.target_size == 2
    error_message = "Shadow mode must keep the worker MIG at Terraform's reviewed floor."
  }
}

run "shadow_rejects_generic_gce_autoscaler" {
  command = plan

  variables {
    autoscaler = { size_max = 15, cpu_target = 0.7, memory_target = 90 }
  }

  expect_failures = [google_compute_region_instance_group_manager.pool]
}

run "network_stage_keeps_worker_source_resolved" {
  command = plan

  variables {
    autoscaler = { size_max = 2, cpu_target = 1, memory_target = 100 }
  }

  assert {
    condition     = google_compute_instance_template.template.disk[0].source_image == data.google_compute_image.source_image.id
    error_message = "Opening the network-stage operator guard must leave the worker source image resolved during planning."
  }

  assert {
    condition     = lookup(google_compute_instance_template.template.metadata, "enable-oslogin", null) == null
    error_message = "The network stage must not enable OS Login on worker or build templates."
  }
}

run "worker_stage_requires_operator_access" {
  command = plan

  variables {
    autoscaler                         = { size_max = 2, cpu_target = 1, memory_target = 100 }
    enable_os_login                    = true
    os_login_operator_access_confirmed = false
  }

  expect_failures = [google_compute_instance_template.template]
}

run "worker_stage_enables_os_login" {
  command = plan

  variables {
    autoscaler      = { size_max = 2, cpu_target = 1, memory_target = 100 }
    enable_os_login = true
  }

  assert {
    condition     = google_compute_instance_template.template.metadata["enable-oslogin"] == "TRUE"
    error_message = "The reviewed worker/build stages must still add OS Login metadata."
  }
}
