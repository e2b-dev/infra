resource "terraform_data" "runtime_credential_guard" {
  input = ""

  lifecycle {
    precondition {
      # This is deliberately impossible to enable with a variable. The patch
      # that completes and verifies keyless runtime credentials must remove
      # this guard as part of the reviewed migration.
      condition     = length(var.gcp_project_id) == -1
      error_message = <<-EOT
        Full GCP deployment is blocked until the attached-service-account and
        Workload Identity runtime migration is implemented and verified. Use
        the foundation-only plan/apply targets.
      EOT
    }
  }
}

# These legacy modules cannot use module-level depends_on because they contain
# local provider configurations. Threading the guard output through an existing
# required input preserves their resource addresses while making targeted plans
# traverse the hard-fail guard.
locals {
  runtime_guarded_gcp_project_id = "${terraform_data.runtime_credential_guard.output}${var.gcp_project_id}"
}
