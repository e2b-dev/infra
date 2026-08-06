mock_provider "nomad" {}

variables {
  update_stanza                    = true
  client_proxy_count               = 2
  available_host_count             = 2
  client_proxy_update_max_parallel = 1
  node_pool                        = "api"
  proxy_port                       = 3002
  health_port                      = 3001
  image                            = "example.invalid/client-proxy@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}

run "fills_two_host_api_pool_without_unschedulable_canary" {
  command = plan

  assert {
    condition     = strcontains(nonsensitive(nomad_job.client_proxy.jobspec), "count = 2")
    error_message = "The two-host invited-beta API pool must render two client-proxy allocations."
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.client_proxy.jobspec), "canary           = 0")
    error_message = "A static-port job that fills the API pool must roll one allocation at a time without an unschedulable third canary."
  }

  assert {
    condition     = !strcontains(nonsensitive(nomad_job.client_proxy.jobspec), "auto_promote")
    error_message = "A zero-canary rolling update must not render canary promotion."
  }
}

run "uses_spare_host_for_singleton_canary" {
  command = plan

  variables {
    client_proxy_count = 1
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.client_proxy.jobspec), "canary           = 1")
    error_message = "A singleton client-proxy with one spare API host should retain zero-downtime canary promotion."
  }

  assert {
    condition     = strcontains(nonsensitive(nomad_job.client_proxy.jobspec), "auto_promote     = true")
    error_message = "A rendered canary must still auto-promote after becoming healthy."
  }
}

run "rejects_more_static_port_replicas_than_hosts" {
  command = plan

  variables {
    client_proxy_count = 3
  }

  expect_failures = [nomad_job.client_proxy]
}

run "rejects_two_at_once_without_a_serving_replica" {
  command = plan

  variables {
    client_proxy_update_max_parallel = 2
  }

  expect_failures = [nomad_job.client_proxy]
}
