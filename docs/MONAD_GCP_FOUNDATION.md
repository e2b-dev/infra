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
- a private regional Cloud Build source bucket whose objects expire after
  seven days;
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

4. Obtain the Cloudflare token, but do not put it in the environment file.
   The foundation creates its empty Secret Manager container; its version is
   added out-of-band only after the reviewed foundation apply. The workload
   creates a dedicated private Cloud SQL database and publishes its generated
   connection URI into the existing Postgres secret container.
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

The legacy `make plan`, `make apply`, and `make destroy` workload targets are
disabled. The topology policy and its tests do not by themselves authorize a
deployment. Workload mutation remains unavailable until a saved-plan workflow
binds the reviewed source, environment, backend, quota policy, and plan bytes,
reruns the topology check, and requires an exact apply confirmation.

## Workload topology safety (not yet enabled)

The first full workload plan remains blocked until the keyless runtime
credential migration is complete. When that gate is removed, save the plan and
inspect its complete topology before apply:

```bash
make -C iac/provider-gcp workload-plan-check \
  WORKLOAD_PLAN=.tfplan.dev
```

The checked-in operator-canary policy expects three Nomad/Consul servers, one
API node, one build node, one sandbox client node, and no ClickHouse or Loki
node. Server and worker regional MIGs have zero automated surge and replace one
instance in place; the API zonal MIG retains one surge instance. The example
environment fixes those counts, machine types, and disks instead of enabling a
worker autoscaler.

The guard derives every role's fixed size, machine type, vCPU count, disk quota
class, local SSD, public-IP requirement, and rollout surge from
`terraform show -json`. `pd-balanced` and `pd-ssd` both count against the SSD
quota. Unknown instance templates, disk types, standalone VM/disk/address
resources, autoscalers, unresolved values, destructive MIG changes, and
in-place capacity reductions are rejected.

The same guard requires exactly one reviewed Cloud SQL canary and its supporting
Private Services Access range, connection, APIs, service identities, IAM roles,
database, user, password generator, and Secret Manager version. It rejects
unknown or duplicate Cloud SQL/private-service resources, destructive database
changes, public database IPv4, plaintext-capable SSL modes, missing backup/PITR
or deletion protection, and drift from the reviewed shared-core tier and disk
bounds.

The base fleet is six VMs and 26 vCPUs. Two transient scenarios are reviewed:

- an API rollout adds one `e2-standard-4` VM and 200 GB standard PD;
- a Packer image build adds one `n1-standard-4` VM, 10 GB SSD PD, and one
  conservatively reserved public IP.

Those scenarios are mutually exclusive. Never run Packer while any MIG rollout
is active. Adding both at once would require eight VMs and 34 vCPUs, exceeding
the reviewed 32-vCPU limit. The guard takes the maximum usage across the two
serialized scenarios, yielding seven VMs, 30 vCPUs, 470 GB SSD PD, 400 GB
standard PD, 750 GB local SSD, and seven regional public IPs.

The reviewed hard limits are 24 instances, 32 global vCPUs, 500 GB SSD PD,
4,096 GB standard PD, 6,000 GB N1 local SSD, and eight regional public IPs.
The policy and the Packer source are both checked in CI, including the static
Packer machine/disk/IP reserve. Any policy limit change or plan usage drift
fails closed. These values remain a reviewed snapshot rather than live quota
evidence: immediately before apply, re-check every limit and current usage in
the selected project and region. Do not raise the checked-in limits without
updated quota evidence and review.

The capacity limit does not make worker replacement safe. Snapshot or pause
every active sandbox, wait for snapshot uploads to become durable, stop new
placement on the affected Nomad nodes, drain allocations, and verify the MIG
is stable before replacing a worker template. That orchestration is not yet
implemented, so any workload rollout remains an operator-blocked procedure.

After the foundation apply, add the Cloudflare secret version directly over
stdin so its value never enters shell history. Replace `<prefix>` with the
configured prefix (the template default is `e2b-`), paste the value, then send
EOF:

```bash
gcloud secrets versions add <prefix>cloudflare-api-token --data-file=-
```

The Cloudflare version is a workload prerequisite. It is not an input to the
F1.0 foundation plan and its value must not appear in Terraform state.

The operator-canary workload creates a dedicated PostgreSQL 16 Cloud SQL
instance instead of accepting an external connection string:

- it reuses the VPC selected by `network_name` and allocates one private
  services `/24` for the single database type and region;
- it has no public IPv4 address and accepts only encrypted connections;
- it uses the shared-core, zonal `db-f1-micro` tier with a 10 GB HDD that may
  auto-grow only to 20 GB;
- it enables seven retained backups, seven days of PITR logs, and both
  Terraform and GCP deletion protection;
- it creates a dedicated `e2b` database and user with a generated password,
  then writes an IPv4 URI ending in `sslmode=require` as a new version of the
  foundation-created `<prefix>postgres-connection-string` secret.

`db-f1-micro` has no Cloud SQL SLA and is suitable only for the one-workcell
operator canary. The database adds no Compute Engine VM, vCPU, external-IP, or
workcell quota usage, so the reviewed seven-instance/30-vCPU peak is unchanged.
The database password and rendered URI are sensitive Terraform values held in
the access-controlled remote state and Secret Manager; neither is output.

The GCP API defaults are six primary-pool and three auth-pool connections, with
one idle connection in each pool. The always-deployed docker reverse proxy has
two fixed three-connection pools, and the migrator has a fixed four-connection
pool. The admission guard therefore requires exactly one API allocation, no
dashboard API, and caps the configured application-side aggregate at
`6 + 3 + 6 + 4 = 19`. Dashboard API is modeled as 16 connections per
allocation but remains disabled for this canary. This is a conservative fork
policy for the shared-core canary, not a claim about PostgreSQL's server-side
maximum. Cloud SQL sizes PostgreSQL
`max_connections` from instance memory; after provisioning, verify it with
`SELECT setting FROM pg_settings WHERE name = 'max_connections'` before
starting the API. Google's separately documented `db-f1-micro` limit of 20
concurrent **Cloud SQL operations** is an administrative operation limit, not
a PostgreSQL session limit. See [Cloud SQL connection
limits](https://cloud.google.com/sql/docs/postgres/quotas#maximum_concurrent_connections)
and [operations
limits](https://cloud.google.com/sql/docs/postgres/quotas#operations_limit).
Changing pool ceilings within the aggregate budget remains supported. Raising
the aggregate requires a reviewed tier and policy update.

To tear the database down, first make a separately reviewed change that
disables both Terraform and GCP deletion protection, then remove the database
resources while retaining the Private Services Access connection and allocated
range. Terraform abandons the connection and `prevent_destroy` protects the
range, so a broad destroy cannot release an allocation that the producer still
uses. Cloud SQL producer cleanup can take up to four days after instance
deletion. Only after that cleanup and proof that no other managed service uses
the peering may a second reviewed change remove the range from the connection,
delete the allocation, and delete the private connection. Follow Google's
[Private Services Access deletion
order](https://cloud.google.com/vpc/docs/configure-private-services-access#delete-connection);
do not release an in-use allocation or delete the VPC Network Peering directly.

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
  --gcs-source-staging-dir=gs://monad-code-cloud-build-source/source \
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
