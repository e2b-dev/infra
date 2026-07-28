# Monad GCP F1.0 — keyless foundation

This fork deliberately separates a zero-workload GCP foundation from the E2B
runtime cluster. Engram's organisation policy forbids long-lived Google
service-account keys. The fork removes those key resources, and a full cluster
plan is permitted only after a repository guard proves that Monad's runtime
consumers use attached service accounts and Application Default Credentials.

## F1.0 creates

- the Terraform state bucket;
- required project APIs;
- service-account identities without private keys;
- a dedicated Cloud Build image-builder identity with write access only to the
  core Artifact Registry repository and Cloud Logging;
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
# Existing foundations created before the sensitive ACL migration only:
mise exec -- make -C iac/provider-gcp foundation-scrub-legacy-acl-token-state \
  CONFIRM='FORGET LEAKING ACL TOKEN GENERATORS'
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

`foundation-init` is the only pre-plan cloud-resource mutation: it bootstraps
the remote state bucket when absent, then enforces and re-reads its immutable
project and location plus uniform bucket-level access, public access
prevention, Standard storage, versioning, and 30-day soft deletion. It fails
rather than reusing a bucket in another project or location, and it never
suppresses a create error.

Foundations created before the sensitive ACL migration contain two
`random_uuid` state entries whose UUID value is also their non-sensitive
Terraform ID. Terraform can print that ID during refresh or destroy. Before any
post-upgrade plan, the guarded one-time scrub above forgets exactly those two
generator addresses. It never reads an attribute, destroys a cloud object, or
touches any other state address. Every supported refresh/plan/apply path refuses
to continue while either address remains.

The following reviewed foundation apply derives UUID-shaped Consul and Nomad
tokens from sensitive `random_password` seeds, creates new latest Secret
Manager versions, and moves the old version resources to explicit legacy
addresses. The old versions are disabled with a `DISABLE` deletion policy;
their payloads remain ignored and redacted. New foundations create a disabled
placeholder before their active version so `versions/latest` always resolves
to the active token. Do not use `terraform state show` or run a direct plan to
inspect the legacy generators.

The legacy `make init` path remains disabled in the Monad fork. The keyless
runtime migration removes the hard-fail Terraform dependency and gates generic
workload plan/apply/import/move targets with configuration, exact-toolchain,
and repository-wide static-credential checks. `make keyless-runtime-check`
runs that guard directly.

The supported foundation workflow still refuses existing state outside
`module.init`, long-lived credential state, changes outside `module.init`, and
any destructive plan. A later patch should split foundation and workload into
separate Terraform roots/states, removing the need for `-target`.

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
- No legacy ACL `random_uuid` generator exists in state.
- No workload VM or external IP exists.
- ACL values are sensitive, UUID-shaped, and redacted from human plan output.
- Out-of-band secret values are absent from Git and plan output.
- The plan contains only reviewed foundation resources.
- A destroy plan has been inspected.

The runtime migration is code-complete but is not live evidence. The next
milestone is a reviewed smallest-fleet workload plan, image build, and stock SDK
create/pause/resume/fork canary. That canary must prove metadata-server ADC,
signed uploads, registry push/pull, snapshot persistence, ClickHouse backup,
and credential refresh before production promotion.

## Build the control-plane images

Do not build the required amd64 images on the operator's Docker Desktop. Submit
the checked-out source to Cloud Build using the dedicated keyless identity:

```bash
revision="$(git rev-parse --short=12 HEAD)"
gcloud builds submit \
  --project=monad-code \
  --region=us-east4 \
  --config=cloudbuild.core-images.yaml \
  --substitutions="_REVISION=${revision}" \
  .
```

The build publishes `api`, `db-migrator`, `client-proxy`,
`docker-reverse-proxy`, and `clickhouse-migrator` with both `latest` and the
source revision tag. Terraform resolves these images before the first workload
plan. Cloud Build can impersonate only `e2b-image-builder`; that identity can
write the core repository and Cloud Logging, while runtime VMs retain
read-only registry access.
