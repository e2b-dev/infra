# Monad GCP keyless and quota evidence — 2026-08-03

## Scope and identity

- Project: `monad-code`.
- Region/zone: `us-east4` / `us-east4-c`.
- Audit principal: `yasser@engram.org` through the local `gcloud` OAuth flow.
- Deployed infra revision under audit:
  `6814caa9c1480507696e7a7e54feeaad03f23c38`.
- Post-rollout revalidation completed at `2026-08-03T08:19:37Z` after the
  three-server, two-API, two-client, one-build topology reached its bounded
  readiness gate.
- Checks were read-only. No secret values or log payloads were printed or
  retained as evidence.

## Service-account and ADC evidence

`gcloud iam service-accounts list` returned three service accounts. Listing
keys for every account with `--managed-by=user` returned **zero user-managed
service-account keys**.

All eight live GCE hosts are running with the attached service account
`e2b-infra-instances@monad-code.iam.gserviceaccount.com`:

- 3 Nomad/control servers.
- 2 API nodes.
- 2 client/worker nodes.
- 1 fixed build node.

The project IAM policy has no member bound to
`roles/iam.serviceAccountKeyAdmin`. Instance metadata contains only the
expected topology/startup keys (`api_cluster`, `cluster-size`, `created-by`,
`enable-guest-attributes`, `enable-osconfig`, `instance-template`, and
`startup-script`). Project common metadata contains the existing `ssh-keys`
entry; it is not a service-account credential.

## Logs, secrets, artifacts, and GitHub configuration

Cloud Logging was queried from `2026-07-04T00:00:00Z` through the revalidation
time.
Each query returned zero matches (bounded at 100 results):

- `BEGIN PRIVATE KEY` markers.
- `private_key_id` service-account JSON markers.
- `GOOGLE_APPLICATION_CREDENTIALS` key-file markers.
- `google.iam.admin.v1.CreateServiceAccountKey` audit events.

Secret Manager names contain no service-account, credential, private-key, JSON
keyfile, or keyfile-shaped entry. Fourteen GCS buckets and 1,800 recursively
listed objects were inspected by name; no object name is GCP
service-account/keyfile-shaped.

The TAMS `dev` environment exposes the E2B configuration only as environment
variables plus the E2B API secret. No `dev` or repository GitHub secret name is
GCP/service-account/keyfile-shaped. The repository-level
`MONAD_GITHUB_APP_PRIVATE_KEY` is a GitHub App signing credential, not a GCP
service-account key, and is outside the GCP ADC replacement claim.

## Live quota headroom

The `us-east4` regional quota snapshot was:

| Metric | Limit | Usage | Available |
| --- | ---: | ---: | ---: |
| CPUs | 3,000 | 38 | 2,962 |
| Instances | 32 | 8 | 24 |
| In-use addresses | 16 | 0 | 16 |
| SSD total GiB | 40,960 | 360 | 40,600 |
| Local SSD total GiB | effectively unlimited | 1,125 | effectively unlimited |

The 32-instance limit covers the reviewed maximum of 15 worker hosts, six fixed
nodes (three control, two API, and one build), and one concurrent replacement:
22 total instances. No `IN_USE_ADDRESSES` increase is justified by the live
snapshot or the reviewed topology. A quota request remains mandatory if a
saved Terraform plan later proves a measured shortfall.

The independent post-rollout Terraform 1.7.5 cluster plan at infra revision
`6814caa9c1480507696e7a7e54feeaad03f23c38` reported `No changes`. Its topology,
cluster-mutation scope, release-artifact, and live-quota guards passed before
the shared mutation lease was released.

## Result and remaining security action

The audited GCP runtime is keyless: GCE consumers use attached-service-account
ADC, with zero user-managed GCP service-account keys and zero observed runtime
key files or log markers.

This result does not close the separate historical Nomad ACL argument exposure
in serial-console logs from the pre-hardening startup path. The next security
change must move ACL retrieval to Secret Manager through the attached service
account and rotate those ACL credentials after the new boot path is deployed.
That credential is not a GCP service-account key and does not change the
keyless-GCP result above.
