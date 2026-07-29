# Monad GCP E2B canary status

Last verified: 2026-07-29 (Australia/Melbourne)

This file is an operator checkpoint for the first `dev` workcell canary. It
records evidence and the safe resume boundary; it is not a claim that TAMS is
already using GCP.

## Completed

- Keyless GCP foundation is applied in project `monad-code`, region
  `us-east4`, with workload zone `us-east4-c`.
- Core E2B release revision is pinned to `909df5f3fea8`.
- GCE image `e2b-orch-dev-candidate-a52586eafdec` passed disposable Docker,
  Nomad, Consul, and nested-KVM smoke tests.
- Canonical image family `e2b-orch` resolves to exact image ID
  `5598366152540933019`.
- No transient Packer/smoke instances or rollout mutation lease remain.
- The three prerequisite Nomad binaries were built once as Linux amd64 from
  exact core revision `909df5f3fea8` and uploaded into the previously empty
  `gs://monad-code-fc-env-pipeline` bucket.
- Infrastructure changes through PR #21 are merged at exact infra SHA
  `fa379a14d3`.
- The guarded cluster resources are applied: three Nomad servers, one API VM,
  one build VM, and one client VM. ClickHouse and Loki remain intentionally
  zero-sized for the operator canary.
- PR #19 pins only the quota-bounded development MIGs to `us-east4-c`; PR #20
  removes the development client bootstrap dependency on phase-two Nomad
  metadata; PR #21 permits only recognised create-before-destroy instance
  template rollouts.
- The zone-scoped Cloudflare credential has an enabled Secret Manager version.
- Live post-cluster quota validation passed at this checkpoint. The tightest
  resource was regional public IP headroom: two were available and one was
  reserved for the reviewed rollout. Re-read every live quota immediately
  before planning and applying.

| Object | Generation | Size | GCS MD5 |
|---|---:|---:|---|
| `orchestrator` | `1785232442089508` | `129278600` | `PMSUl0V18Ei9Lyj4jlVgag==` |
| `orchestrator.909df5f3fea8` | `1785232866521885` | `129278600` | `PMSUl0V18Ei9Lyj4jlVgag==` |
| `template-manager` | `1785232472561265` | `129278600` | `PMSUl0V18Ei9Lyj4jlVgag==` |
| `template-manager.909df5f3fea8` | `1785232895401036` | `129278600` | `PMSUl0V18Ei9Lyj4jlVgag==` |
| `clean-nfs-cache` | `1785232517773444` | `50557937` | `zLUBMUIvTYcm+aDVzj1ZFA==` |
| `clean-nfs-cache.909df5f3fea8` | `1785232911212631` | `50557937` | `zLUBMUIvTYcm+aDVzj1ZFA==` |

Earlier nine-character revision aliases remain as historical uploads and are
not release inputs.

Local SHA-256:

- `orchestrator`: `16401bbd8a34a697c79f0ded917c1ebc9493c441ed4a10a8bd01e34effb7bd0d`
- `clean-nfs-cache`: `9ff92543bc424576db4ec638fa3c76bef6a5c0a9f4ba32994712ccf0fd709723`

Do not replace the canonical GCS objects between publishing a reviewed
workload plan and consuming that plan.

## Current blocker

The client regional MIG is target reached but not stable. The running instance
still needs the reviewed PR #20 startup template before bounded cluster
readiness can pass.

The saved cluster plan/apply recipe currently invokes `bootstrap` quota mode.
That mode correctly refuses what appears to be a second full fleet because the
cluster already consumes six public IPs and 260 GiB of SSD quota. The separate
`post-cluster` mode correctly validates only the reviewed replacement reserve
and currently passes:

| Quota | Available | Required reserve |
|---|---:|---:|
| Global vCPU | 38 | 4 |
| Instances | 26 | 1 |
| Regional CPU | 174 | 4 |
| SSD persistent disk | 240 GiB | 10 GiB |
| Standard persistent disk | 3,896 GiB | 200 GiB |
| Regional public IP | 2 | 1 |

Select bootstrap versus post-cluster quota mode from the reviewed
plan/state transition. Preserve initial-create and genuine-overcommit
rejection, and test initial create, no-op, safe replacement, and unsafe
replacement before applying the template.

## Resume boundary

No Cloud SQL instance, phase-two Nomad workload jobs, or successful SDK
lifecycle canary exists yet. Resume in this order:

1. merge the tested bootstrap/post-cluster quota-mode selection;
2. publish and review the exact client-template replacement plan;
3. apply only those reviewed bytes with the exact canary confirmation;
4. require every MIG, DNS/TLS check, the three-server Nomad control plane, and
   the required clients to pass the bounded cluster readiness gate;
5. publish, review, and apply the full workload plan, then verify public API,
   Nomad jobs, migrations, network, metadata, logs, and cleanup;
6. publish a unique canary team/key helper with explicit success-path deletion
   of the `team_api_keys` row, team, and Secret Manager secret plus crash
   reconciliation from persisted identifiers; never print the key or use the
   fixed, destructive seed helper, and store the credential through Secret
   Manager stdin;
7. build and pin the immutable Monad base template;
8. pin and lock exact `e2b@2.21.0`; use `Sandbox.connect` to resume,
   `Sandbox.kill` to destroy, create from `snapshot.snapshotId` to prove
   restore, and delete the explicit snapshot. Use
   `POST /sandboxes/{sandboxId}/fork` with `X-API-Key` and
   `{"count":1,"timeout":900}`, require HTTP 201, validate every result entry,
   and prove source/fork divergence;
9. add trusted placement attestation and the durable operator execution seam
   before registering E2B for synthetic TAMS work.

`local_docker` remains development/emergency-recovery infrastructure and is
not the beta execution target. Keep
`ALLOWED_SANDBOX_PROVIDERS=local_docker`; no real repository credential,
customer data, or beta-default traffic enters this canary. The canary admission
must permit exactly two concurrent sandboxes for lifecycle proof. Never exceed
source plus one child: kill each fork or snapshot-restored sandbox before
creating another, then kill the source and delete every explicit snapshot.
