# Monad GCP static-key re-audit — 2026-08-05

## Scope and handling rules

- Project: `monad-code`; workload region/zone: `us-east4` / `us-east4-c`.
- Read-only audit principal: `yasser@engram.org` through short-lived local
  `gcloud` OAuth.
- Audit interval: `2026-08-05T14:11:18Z` through
  `2026-08-05T14:35:39Z`.
- Audited source revisions after a final remote refresh:
  - `monad-e2b-infra` `origin/main`:
    `97d88ab16b63e4ad229fdc431b4bb14c30524f1c`.
  - TAMS `origin/main`:
    `8230ee3082a46882e0ff905bada245bd48cf8059`.
- The current dev Terraform state object was last updated at
  `2026-08-03T09:09:58Z`; the prior deployment evidence records applied infra
  revision `6814caa9c1480507696e7a7e54feeaad03f23c38`. This re-audit did
  not plan, apply, rotate, or otherwise mutate infrastructure.

No Secret Manager version was accessed. No Terraform state value, object
payload, log payload, GitHub variable value, GitHub secret value, metadata
token, or key material was printed or retained. State inspection emitted only
resource addresses; Cloud Logging responses were server-filtered and
field-limited to entry IDs and timestamps; GCS inspection listed object
metadata only.

## Result

The live GCP runtime remains keyless for Google service-account identity:

- all three project service accounts have zero user-managed keys;
- all eight running hosts use the attached
  `e2b-infra-instances@monad-code.iam.gserviceaccount.com` identity;
- every host returned a valid short-lived metadata-ADC token without exposing
  the token;
- current Terraform state contains no Google service-account-key resource
  address, and both exact source trees contain no such payload;
- bounded runtime roots, instance metadata, startup scripts, log indexes,
  Secret Manager metadata, GCS object metadata, Artifact Registry metadata,
  and GitHub configuration names contain no GCP service-account-key seam.

This is a point-in-time GCP identity result, not a claim that every platform
credential is keyless. TAMS retains named AWS/Lightsail and GitHub App secret
containers, and the GCP project retains common `ssh-keys` metadata. Those are
not Google service-account keys and no value was inspected. The effective
organisation policies returned no explicit service-account key-creation or
key-upload prohibition, so future privileged key creation is not prevented by
an observed org-policy rule; the current compensating controls are zero keys,
zero key-mutation audit events, no explicit project Key Admin or Service
Account Admin binding, and the repository/CI guards.

## IAM, attached identity, and live hosts

`gcloud iam service-accounts list` returned exactly:

- `883250301766-compute@developer.gserviceaccount.com`;
- `e2b-image-builder@monad-code.iam.gserviceaccount.com`;
- `e2b-infra-instances@monad-code.iam.gserviceaccount.com`.

For each account, `gcloud iam service-accounts keys list --managed-by=user`
returned zero. Project IAM has zero members on `roles/iam.serviceAccountKeyAdmin`
and `roles/iam.serviceAccountAdmin`. The runtime account has its reviewed
self-signing `roles/iam.serviceAccountTokenCreator` service-account policy;
the image-builder account has two members on the same service-account policy.

The fleet inventory was three control servers, two API nodes, two Firecracker
workers, and one fixed build node. Every instance has the same attached runtime
account and the reviewed scopes: `cloud-platform`, `compute.readonly`,
`logging.write`, `monitoring.write`, `trace.append`, and `userinfo.email`.
All eight IAP SSH checks returned the attached email plus only these safe token
facts: token present, type `Bearer`, and positive expiry.

A bounded root-side scan covered 8,853 candidate text/config files smaller
than 2 MiB across `/etc`, `/opt`, `/root`, and `/home`, pruning caches, logs,
template/snapshot/build payload trees, dependencies, and vendor trees. It
found zero service-account/keyfile-shaped filenames, zero service-account JSON
or former key-variable content paths, and zero former key-shaped process
environment names on every host. Large sandbox/template artifacts were not
opened; their GCS metadata is covered separately below.

## Terraform, instance templates, images, and metadata

Terraform 1.7.5 initialized a temporary read-only backend client against
`gs://monad-code-terraform-state` at
`terraform/orchestration/dev/state`. `terraform state list` emitted 276
addresses: zero service-account-key or storage-HMAC-key resources, zero legacy
Consul/Nomad UUID generators, and two service-account resources. The temporary
backend files and address-only output were discarded after counting.

The current state path has 50 retained object generations. The latest is
generation `1785748198490892`, 745,512 bytes, updated
`2026-08-03T09:09:58Z`. Only generation metadata was read; historical state
payloads were not downloaded. The state bucket remains Standard in
`US-EAST4`, with uniform access, public-access prevention, versioning, and a
30-day soft-delete policy.

All eight instance metadata records and all six instance-template metadata
records have zero static-key marker matches. Their startup-script SHA-256
digests collapse to the expected four role-specific values:

| Role | SHA-256 |
| --- | --- |
| API | `ee76bef702b38fb374c74bb658ea9cff99e7fb5ef68626024fc28891725c8be2` |
| Build | `7015765589f4a3966a62eb0da6994ae70d8e464f967109dd53c05e619d23b965` |
| Worker | `2fffade12481c5184c1d9c8f56cc68ffea14fcb8e13c38734f4303bb47f1c3c9` |
| Control | `9ebea0897f4919bd6184e5550335b7c285195c1759ffd62d0c74c9ff531eaca8` |

The only project GCE image is
`e2b-orch-dev-candidate-a52586eafdec`; its labels, description, family, and
guest-feature metadata have zero key-shaped matches. Project common instance
metadata contains only the pre-existing `ssh-keys` entry.

## Logs, secrets, and artifacts

The Logging `entries:list` API was queried from `2026-07-04T00:00:00Z`
through `2026-08-05T14:17:04Z`, requesting only entry IDs, timestamps, and
page tokens. Complete pagination returned:

- zero create, upload, delete, disable, or enable service-account-key audit
  events across four pages;
- zero private-key, service-account JSON, application-credentials, or former
  runtime-key marker matches across two pages.

Secret Manager has 32 containers and 34 version metadata records: 32 enabled,
two disabled legacy ACL versions, and zero destroyed. Container names and
labels contain zero GCP service-account/keyfile-shaped names, and all 32
containers have zero direct secret-level IAM bindings. No payload access
command was used.

All 14 GCS buckets were listed with all live/noncurrent object-generation
metadata. Across 2,505 object generations there are zero GCP
service-account/keyfile-shaped object names, zero such custom-metadata key
names, and zero customer-supplied encryption records. No object payload was
read. The three Artifact Registry repositories and 33 `e2b-core` Docker image
metadata rows likewise contain zero keyfile-shaped repository, image, or tag
names.

## GitHub and repository evidence

Only configuration names and update metadata were listed:

| Repository/scope | Variables | Secrets | GCP key-shaped names |
| --- | ---: | ---: | ---: |
| `monad-e2b-infra` repository | 0 | 0 | 0 |
| `monad-e2b-infra` environments | none | none | 0 |
| TAMS repository | 25 | 48 | 0 |
| TAMS `dev` environment | 27 | 8 | 0 |

The TAMS `dev` environment contains the E2B API credential container and the
immutable template variables, but no GCP service-account credential name. The
repository-level GitHub App private-key container and AWS/Lightsail containers
are explicitly outside this GCP ADC claim.

Path-only scans of the exact source trees covered 2,385 tracked infra files
and 4,409 tracked TAMS files. Neither tree contains a service-account JSON
payload or a tracked keyfile-shaped JSON filename. Infra has no PEM payload.
TAMS has one PEM marker path in `tests/shell/vps/test-ssh-e2e.sh`; it is an
assertion-only shell fixture (no base64-like body line over 64 characters),
not key material. The two TAMS references to former Google credential naming
are generated catalog documentation and the beta plan, not runtime inputs.

Local validation at the final infra head:

```text
make -C iac/provider-gcp keyless-runtime-check
Keyless GCP runtime guard tests passed.
Keyless Docker helper configuration test passed.
Control-plane secret logging regression test passed.
Legacy ACL token state scrub fixtures passed.
```

Exact-head CI also passed:

- infra `Keyless GCP configuration`, run
  `31015534485`, at `97d88ab16b63e4ad229fdc431b4bb14c30524f1c`;
- TAMS `gitleaks`, run `31014586284`, at
  `8230ee3082a46882e0ff905bada245bd48cf8059`.

## Reproduction boundary

Repeat only the same metadata-safe commands: list service accounts and
user-managed key identities, list attached instance accounts/scopes and
metadata keys, use address-only `terraform state list`, query Logging with
response field selection, list Secret Manager names/version states/IAM,
list GCS and Artifact Registry metadata, and list GitHub configuration names.
Do not use Secret Manager access, Terraform state show/pull, object download,
log payload output, GitHub value retrieval, or metadata-token output while
reproducing this evidence.
