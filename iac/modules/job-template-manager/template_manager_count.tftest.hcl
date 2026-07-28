mock_provider "nomad" {}

mock_provider "external" {
  mock_data "external" {
    defaults = {
      result = {
        count = "5"
      }
    }
  }
}

run "fixed_worker_bootstrap" {
  command = plan

  variables {
    node_pool       = "build"
    port            = 5008
    update_stanza   = false
    artifact_source = "gcs::https://example.test/template-manager"
    nomad_addr      = "https://nomad.example.test"
    nomad_token     = "redacted"
  }

  assert {
    condition     = length(data.external.template_manager_count) == 0
    error_message = "Fixed one-worker topology must not query live Nomad state."
  }

  assert {
    condition     = strcontains(nomad_job.template_manager.jobspec, "count = 1")
    error_message = "Fixed one-worker topology must render exactly one template manager."
  }
}

run "scaled_worker_preserves_live_count" {
  command = plan

  variables {
    node_pool       = "build"
    port            = 5008
    update_stanza   = true
    artifact_source = "gcs::https://example.test/template-manager"
    nomad_addr      = "https://nomad.example.test"
    nomad_token     = "redacted"
  }

  assert {
    condition     = length(data.external.template_manager_count) == 1
    error_message = "Scaled topology must retain exactly one live Nomad count lookup."
  }

  assert {
    condition     = strcontains(nomad_job.template_manager.jobspec, "count = 5")
    error_message = "Scaled topology must render the live autoscaler-managed count."
  }

  assert {
    condition     = strcontains(nomad_job.template_manager.jobspec, "min     = 2")
    error_message = "Scaled topology must retain the two-worker minimum."
  }
}
