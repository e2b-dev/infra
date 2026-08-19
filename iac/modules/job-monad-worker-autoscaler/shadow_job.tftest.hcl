mock_provider "nomad" {}

variables {
  node_pool           = "api"
  worker_node_pool    = "default"
  worker_cluster_keys = ["default"]
  worker_cluster_size = 6
  worker_machine_type = "n1-standard-8"
  worker_host_floor   = 2
  allocation_count    = 2
  artifact_source     = "gcs::https://www.googleapis.com/storage/v1/monad-code-fc-env-pipeline/monad-worker-autoscaler.0123456789ab#123"
  tams_capacity_url   = "https://api.tams.monad0.net/v1/ops/capacity"
  tams_audience       = "https://api.tams.monad0.net"
  nomad_token         = "test-nomad-token"
  consul_token        = "test-consul-token"
}

run "renders_two_non_mutating_observers" {
  command = plan

  assert {
    condition     = strcontains(nonsensitive(nomad_job.controller.jobspec), "count = 2")
    error_message = "The beta topology must render two redundant observers."
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.controller.jobspec), "MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED = \"false\"")
    error_message = "The rendered shadow job must explicitly disable mutation."
  }

  assert {
    condition     = !strcontains(lower(nonsensitive(nomad_job.controller.jobspec)), "google_compute")
    error_message = "The shadow Nomad job must not contain a GCE mutation seam."
  }

  assert {
    condition     = !strcontains(nonsensitive(nomad_job.controller.jobspec), "MIG_NAME")
    error_message = "The shadow Nomad job must not name a resize target."
  }

  assert {
    condition     = !strcontains(nonsensitive(nomad_job.controller.jobspec), "TAMS_OPS_TOKEN") && !strcontains(lower(nonsensitive(nomad_job.controller.jobspec)), "tams_token")
    error_message = "The rendered job must mint attached-service-account identity tokens and contain no durable TAMS bearer."
  }
}

run "rejects_cross_origin_identity_delivery" {
  command = plan

  variables {
    tams_audience = "https://attacker.example"
  }

  expect_failures = [nomad_job.controller]
}

run "rejects_mixed_worker_fleet" {
  command = plan

  variables {
    worker_cluster_keys = ["default", "gpu"]
  }

  expect_failures = [nomad_job.controller]
}

run "rejects_shared_nomad_worker_pool" {
  command = plan

  variables {
    worker_node_pool = "shared-workers"
  }

  expect_failures = [nomad_job.controller]
}

run "rejects_wrong_worker_profile" {
  command = plan

  variables {
    worker_cluster_size = 3
    worker_machine_type = "e2-standard-8"
  }

  expect_failures = [nomad_job.controller]
}

run "accepts_rekeyed_reviewed_floor" {
  command = plan

  variables {
    worker_cluster_size = 4
    worker_host_floor   = 4
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.controller.jobspec), "MONAD_WORKER_AUTOSCALER_MODE             = \"shadow\"")
    error_message = "A re-keyed floor must still render the shadow mode."
  }
}

run "rejects_floor_cluster_size_disagreement" {
  command = plan

  variables {
    worker_cluster_size = 2
    worker_host_floor   = 4
  }

  expect_failures = [nomad_job.controller]
}

run "renders_scale_out_actuation" {
  command = plan

  variables {
    mode                = "scale-out"
    worker_cluster_size = 4
    worker_host_floor   = 4
    mig_project_id      = "monad-code"
    mig_region          = "us-east4"
    mig_name            = "e2b-orch-client-rig"
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.controller.jobspec), "MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED = \"scale-out-only\"")
    error_message = "The scale-out job must render the exact double-keyed mutation phrase."
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.controller.jobspec), "MIG_NAME          = \"e2b-orch-client-rig\"") && strcontains(nonsensitive(nomad_job.controller.jobspec), "WORKER_HOST_FLOOR = \"4\"")
    error_message = "The scale-out job must name its resize target and reviewed floor."
  }
}

run "rejects_scale_out_without_resize_target" {
  command = plan

  variables {
    mode                = "scale-out"
    worker_cluster_size = 4
    worker_host_floor   = 4
    mig_project_id      = "monad-code"
    mig_region          = "us-east4"
  }

  expect_failures = [nomad_job.controller]
}

run "rejects_shadow_with_resize_target_leftovers" {
  command = plan

  variables {
    mig_name = "e2b-orch-client-rig"
  }

  expect_failures = [nomad_job.controller]
}

run "rejects_unknown_mode" {
  command = plan

  variables {
    mode = "mutate"
  }

  expect_failures = [var.mode]
}
