# Monad GCP F1.0 — keyless foundation

This fork deliberately separates a zero-workload GCP foundation from the E2B
runtime cluster. Engram's organisation policy forbids long-lived Google
service-account keys. The fork removes those key resources, and a full cluster
plan is blocked until all runtime consumers use attached service accounts,
Application Default Credentials, or Workload Identity.

## F1.0 creates

- the Terraform state bucket;
- required project APIs;
- service-account identities without private keys;
- Secret Manager containers and generated control-plane secrets;
- Artifact Registry repositories;
- GCS buckets and their IAM bindings.

It does not create the Nomad/Consul cluster, API nodes, ClickHouse, build nodes,
sandbox nodes, workload VMs, or Packer image.

## Prerequisites

1. Authenticate both the CLI and Terraform:

   ```bash
   gcloud auth login
   gcloud auth application-default login
   gcloud config set project monad-code
   ```

2. Install the versions pinned in `.tool-versions`. Run the workflow through
   `mise exec` so both Terraform and gcloud resolve to those reviewed versions.

3. Copy `.env.gcp.template` to an ignored environment file and set only
   non-secret deployment metadata there.

4. Obtain the Cloudflare token and Postgres connection string, but do not put
   either value in the environment file. The foundation creates their empty
   Secret Manager containers; versions are added out-of-band only after the
   reviewed foundation apply.
5. Request the documented regional SSD quota before the workload phase.
   The current `us-east4` instance quota is 24; model the reviewed smallest
   fleet and request headroom before any workload plan exceeds it.

## Reviewable workflow

```bash
make set-env ENV=dev
mise exec -- make -C iac/provider-gcp foundation-init
mise exec -- make -C iac/provider-gcp foundation-plan
mise exec -- terraform -chdir=iac/provider-gcp show .tfplan.foundation.dev
mise exec -- make -C iac/provider-gcp foundation-apply \
  CONFIRM='APPLY KEYLESS FOUNDATION'
mise exec -- make -C iac/provider-gcp foundation-destroy-plan
mise exec -- terraform -chdir=iac/provider-gcp show .tfplan.foundation-destroy.dev
rm -f iac/provider-gcp/.tfplan.foundation-destroy.dev
```

`foundation-plan` targets only `module.init`. `foundation-apply` consumes the
exact saved plan and requires a literal confirmation. It never invokes Packer
and forces Anywhere Cache off even if an environment file says otherwise.
Each environment uses an explicit backend prefix at
`terraform/orchestration/<environment>/state`. Plan and apply fail unless the
initialized backend metadata matches both the selected state bucket and that
environment-specific prefix. Plan, apply, and destroy-plan also re-read the
live bucket and fail if its project, location, storage class, uniform access,
public-access prevention, versioning, or 30-day soft-delete policy has drifted.
The workflow requires Terraform's `default` workspace because environment
isolation is provided by the explicit backend prefix instead.

`foundation-init` is the only pre-plan cloud mutation: it bootstraps the remote
state bucket when absent, then enforces and re-reads its immutable project and
location plus uniform bucket-level access, public access prevention, Standard
storage, versioning, and 30-day soft deletion. It fails rather than reusing a
bucket in another project or location, and it never suppresses a create error.

The legacy `make init` path is disabled in the Monad fork. Workload modules
depend on a hard-fail credential guard which has no variable escape hatch. The
patch that completes and verifies the keyless migration must remove that guard
as an explicit reviewed change.

All generic workload plan/apply/import/move Make targets are disabled during
F1.0, including stale saved-plan apply paths. The supported foundation workflow
also refuses existing state outside `module.init`, long-lived credential state,
changes outside `module.init`, and any destructive plan. Direct targeted
Terraform commands are unsupported in this phase. A later patch should split
foundation and workload into separate Terraform roots/states, removing the
need for `-target`.

After the foundation apply, add the first secret versions directly over stdin
so values never enter shell history. Replace `<prefix>` with the configured
prefix (the template default is `e2b-`), paste one value, then send EOF:

```bash
gcloud secrets versions add <prefix>cloudflare-api-token --data-file=-
gcloud secrets versions add <prefix>postgres-connection-string --data-file=-
```

These versions are workload prerequisites. They are not inputs to the F1.0
foundation plan and their values must not appear in Terraform state.

Saved Terraform plans contain cleartext configuration and input values,
including sensitive values. `foundation-plan` creates its plan with mode 0600.
It first invalidates any previous plan, publishes the replacement atomically,
and writes a 0600 provenance manifest binding the plan digest to the Git commit,
Terraform source and lock file, environment inputs, project, region, backend,
and exact Terraform version. Apply recomputes that manifest and refuses any
source, input, backend, toolchain, or plan-byte drift.
Terraform's implicit `terraform.tfvars`, `terraform.tfvars.json`, and
`*.auto.tfvars*` inputs are rejected; reviewed optional values belong only in
the explicit `.terraform.<environment>.tfvars` file. The saved plan must also
report the selected project, region, and environment before it can be
published or applied.
Keep it on the trusted operator machine, do not upload it or persist
`terraform show -json` output, and remove an abandoned plan with:

```bash
rm -f iac/provider-gcp/.tfplan.foundation.dev
rm -f iac/provider-gcp/.tfplan.foundation.dev.manifest
rm -f iac/provider-gcp/.tfplan.foundation-destroy.dev
```

## Exit evidence

- Terraform state is remote, versioned, and recoverable.
- No `google_service_account_key` resource exists in state.
- No workload VM or external IP exists.
- Secret values are out-of-band and absent from Git and plan output.
- The plan contains only reviewed foundation resources.
- A destroy plan has been inspected.

The next milestone is the keyless runtime credential migration, followed by the
smallest reference fleet and stock SDK create/pause/resume/fork canary.
