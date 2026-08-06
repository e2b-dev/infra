mock_provider "nomad" {}

variables {
  nomad_token                  = "test-nomad-token"
  consul_token                 = "test-consul-token"
  ingress_port                 = 8800
  ingress_internal_port        = 8801
  node_pool                    = "api"
  update_stanza                = true
  ingress_count                = 2
  available_host_count         = 2
  otel_collector_grpc_endpoint = "localhost:4317"
  traefik_config_files         = {}
}

run "fills_two_host_api_pool_without_unschedulable_canary" {
  command = plan

  assert {
    condition     = strcontains(nonsensitive(nomad_job.ingress.jobspec), "count = 2")
    error_message = "The two-host invited-beta API pool must render two ingress allocations."
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.ingress.jobspec), "canary           = 0")
    error_message = "A static-port job that fills the API pool must roll one allocation at a time without an unschedulable third canary."
  }

  assert {
    condition     = !strcontains(nonsensitive(nomad_job.ingress.jobspec), "auto_promote")
    error_message = "A zero-canary rolling update must not render canary promotion."
  }
}

run "uses_spare_host_for_singleton_canary" {
  command = plan

  variables {
    ingress_count = 1
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.ingress.jobspec), "canary           = 1")
    error_message = "A singleton ingress with one spare API host should retain zero-downtime canary promotion."
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.ingress.jobspec), "auto_promote     = true")
    error_message = "A rendered canary must still auto-promote after becoming healthy."
  }
}

run "rejects_more_static_port_replicas_than_hosts" {
  command = plan

  variables {
    ingress_count = 3
  }

  expect_failures = [nomad_job.ingress]
}
