mock_provider "nomad" {}

variables {
  node_pool           = "api"
  worker_node_pool    = "default"
  worker_cluster_keys = ["default"]
  worker_cluster_size = 6
  worker_machine_type = "n1-standard-8"
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
    condition     = strcontains(nonsensitive(nomad_job.shadow.jobspec), "count = 2")
    error_message = "The beta topology must render two redundant observers."
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.shadow.jobspec), "MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED = \"false\"")
    error_message = "The rendered job must explicitly disable mutation."
  }

  assert {
    condition     = !strcontains(lower(nonsensitive(nomad_job.shadow.jobspec)), "google_compute")
    error_message = "The shadow Nomad job must not contain a GCE mutation seam."
  }

  assert {
    condition     = !strcontains(nonsensitive(nomad_job.shadow.jobspec), "TAMS_OPS_TOKEN") && !strcontains(lower(nonsensitive(nomad_job.shadow.jobspec)), "tams_token")
    error_message = "The rendered job must mint attached-service-account identity tokens and contain no durable TAMS bearer."
  }
}

run "rejects_cross_origin_identity_delivery" {
  command = plan

  variables {
    tams_audience = "https://attacker.example"
  }

  expect_failures = [nomad_job.shadow]
}

run "rejects_mixed_worker_fleet" {
  command = plan

  variables {
    worker_cluster_keys = ["default", "gpu"]
  }

  expect_failures = [nomad_job.shadow]
}

run "rejects_shared_nomad_worker_pool" {
  command = plan

  variables {
    worker_node_pool = "shared-workers"
  }

  expect_failures = [nomad_job.shadow]
}

run "rejects_wrong_worker_profile" {
  command = plan

  variables {
    worker_cluster_size = 3
    worker_machine_type = "e2-standard-8"
  }

  expect_failures = [nomad_job.shadow]
}
