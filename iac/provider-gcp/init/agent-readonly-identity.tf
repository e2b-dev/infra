# Read-only GCP identity for coding agents (rehoboam-lab orchestrator /
# Claude Code sessions) operating against this E2B control plane.
#
# Operator decision (2026-08-23): agents get READ-ONLY access to inspect the
# control plane -- instance list + log reads -- to unblock live debugging
# without granting apply/mutate capability or SSH. IAP-tunneled SSH is
# explicitly NOT required for this: `gcloud compute instances list` and
# `gcloud logging read` cover the read paths agents actually need, and
# neither needs roles/iap.tunnelResourceAccessor. If a future read-only need
# genuinely requires `gcloud compute ssh --tunnel-through-iap` (e.g. pulling
# a log file that isn't shipped to Cloud Logging), add that role then, with
# its own justification -- don't pre-grant it speculatively.
#
# No keys are issued. Access is impersonation-only: an operator with
# roles/iam.serviceAccountTokenCreator on this SA runs
#   gcloud --impersonate-service-account=agent-readonly@<project>.iam.gserviceaccount.com ...
# which mints short-lived tokens on demand. There is no long-lived credential
# to leak, rotate, or scope-creep.
#
# NOTE on state: this SA and its bindings were first created directly with
# gcloud (see the PR that added this file) because iac/provider-gcp's normal
# apply path requires a provisioned Terraform backend/env this change didn't
# need to touch, and IAM grants here are additive/reversible -- consistent
# with this repo's practice of hand-applying and then registering
# out-of-band changes in Terraform (see the root Makefile's `import` target
# / `iac/provider-gcp/README` for the pattern). Before the next real
# `terraform apply` against this module, run (from iac/provider-gcp/init,
# with -var="agent_readonly_impersonator=user:<operator-email>" set to match
# the live binding, or the plan will propose deleting it):
#   terraform import google_service_account.agent_readonly \
#     projects/<project>/serviceAccounts/agent-readonly@<project>.iam.gserviceaccount.com
#   terraform import 'google_project_iam_member.agent_readonly["roles/compute.viewer"]' \
#     "<project> roles/compute.viewer serviceAccount:agent-readonly@<project>.iam.gserviceaccount.com"
#   terraform import 'google_project_iam_member.agent_readonly["roles/logging.viewer"]' \
#     "<project> roles/logging.viewer serviceAccount:agent-readonly@<project>.iam.gserviceaccount.com"
#   terraform import google_service_account_iam_member.agent_readonly_impersonation[0] \
#     "projects/<project>/serviceAccounts/agent-readonly@<project>.iam.gserviceaccount.com roles/iam.serviceAccountTokenCreator user:<operator-email>"
# Otherwise plan will propose creating duplicates that already exist.
#
# var.agent_readonly_impersonator defaults to "" (module.init isn't wired
# with a value for it yet, and `terraform validate` needs the whole module
# tree to type-check without one) and the impersonation grant is skipped
# entirely -- not granted to nobody/wildcard, simply not created -- while
# it's empty. Thread a real value through (root module variable +
# module "init" argument, or a per-env .tfvars) and set it to the operator's
# actual principal (user:you@example.com or group:agents@example.com)
# before this is actually planned/applied through Terraform; the live
# binding today was applied directly, outside Terraform, to the operator
# who provisioned it (see NOTE on state above).

variable "agent_readonly_impersonator" {
  description = "IAM member (e.g. \"user:name@example.com\" or \"group:agents@example.com\") allowed to impersonate the read-only agent service account via roles/iam.serviceAccountTokenCreator. Empty string (the default) means: create the SA and its read-only project roles, but skip the impersonation grant entirely -- never grant it to nobody or to a wildcard by default."
  type        = string
  default     = ""
}

resource "google_service_account" "agent_readonly" {
  # Deliberately not "${var.prefix}agent-readonly": this identity is a fixed,
  # cross-environment "the agents" account, not a per-workload resource, so
  # it doesn't follow the per-env/per-component prefix convention used
  # elsewhere in this file.
  account_id   = "agent-readonly"
  display_name = "Agent read-only access (compute + logging viewer)"
  description  = "Impersonation-only SA for coding-agent read access to the E2B control plane: instance/list inspection + log reads. No keys issued; bound for impersonation by the operator principal only."
}

locals {
  agent_readonly_project_roles = toset([
    "roles/compute.viewer",
    "roles/logging.viewer",
  ])
}

resource "google_project_iam_member" "agent_readonly" {
  for_each = local.agent_readonly_project_roles

  project = var.gcp_project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.agent_readonly.email}"
}

resource "google_service_account_iam_member" "agent_readonly_impersonation" {
  count = var.agent_readonly_impersonator != "" ? 1 : 0

  service_account_id = google_service_account.agent_readonly.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = var.agent_readonly_impersonator
}
