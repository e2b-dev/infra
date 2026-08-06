# Monad Worker Autoscaler

## Current phase: shadow only

`packages/monad-worker-autoscaler` is the non-mutating foundation for the
invited-beta worker controller. It calculates and publishes the worker-host
recommendation, but it has no GCP SDK, resize target, instance-delete path, or
drain mutation. `MONAD_WORKER_AUTOSCALER_MODE` must be exactly `shadow`; any
truthy `MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED` value makes the process exit.

This phase can run as one or two Nomad allocations on the API pool. With two
allocations, a Consul session lock elects the only allocation allowed to read
capacity and publish decisions. Loss of the lock, Consul, TAMS, or Nomad causes
an immediate hold and breaks any accumulated scale-in window.

The Terraform switch is disabled by default:

- `monad_worker_autoscaler_shadow_enabled=false`
- `monad_worker_autoscaler_revision` must name the immutable uploaded object
  `monad-worker-autoscaler.<12-40 character infra SHA>` when enabled.
- `monad_worker_autoscaler_allocations` is restricted to `1` or `2`.
- the exact TAMS URL and expected Google identity-token audience are required
  only when enabled.
- the provider wiring refuses non-`dev` environments until this beta-specific
  contract and topology are deliberately generalized.
- enabling the job requires exactly one Terraform client cluster named
  `default`, assigned to the Nomad `default` node pool, with the reviewed
  two-host `n1-standard-8` floor.

The Nomad job module adds no IAM grants. The GCP foundation provisions a
dedicated `api-controller` service account, attaches it only to the dev API
pool, and grants the API-pool baseline needed for startup objects, container
pulls, telemetry, Loki, the immutable observer artifact, and the docker
registry proxy. It has no template/build-bucket or service-account-signing
grant. Worker, build, and server pools retain the separate fleet runtime identity. The job
reuses the existing Nomad and Consul ACL tokens for those internal systems. For
TAMS, it mints a short-lived Google ID token from the GCE metadata server and
the API node's attached service account for every capacity request. The
capacity endpoint is origin-bound to the token audience before a token is
minted. The metadata client bypasses proxies;
bearer bytes
are never placed in Terraform, the Nomad job, environment, URL, filesystem,
logs, or artifacts. The Nomad task sets mutation disabled explicitly. A
Terraform precondition also refuses to deploy the shadow observer for the
default worker pool while the generic GCE CPU/memory autoscaler owns the MIG.
During shadow mode Terraform must retain the reviewed two-host target size.
SHA-named controller artifacts are published create-only (`if-generation-match=0`
on GCS and `if-none-match=*` on S3), so rebuilding a revision cannot replace the
generation already pinned in a Nomad job.

## Capacity calculation

The controller recomputes, rather than trusts, the beta formula:

```text
required = active + booting + draining + max(parked, warmTarget)
desiredHosts = clamp(ceil(required / 2) + 1, 2, 15)
```

Two workcells per `n1-standard-8` worker is planned density. Three remains the
hard emergency admission ceiling and is never used by the desired-host
calculation. The additional host is readiness reserve.

The input contract is fixed to the invited-beta envelope: at most 100 durable
sessions, an active limit of 25, and a queue limit of 75. Active plus draining
session workcells may not exceed 25. A snapshot requiring more than the fleet's
45-workcell hard emergency ceiling is rejected instead of being silently
clamped to 15 hosts.

`durable_sessions` counts user session records. Active and draining workcells
belong to durable sessions. In contrast, `booting_workcells` and
`parked_workcells` are provider-native warm-pool rows and can legitimately
exist before any durable session claims them. The controller therefore does
not require booting or parked counts to fit inside the durable-session count;
it still counts both `booting` and `max(parked, warmTarget)` exactly as the
capacity formula specifies.

Worker-host actuality is read independently from Nomad's `default` node pool.
Ready, scheduling-ineligible nodes count as actual and draining. A down node,
unknown status, duplicate identity, wrong pool, or unknown eligibility is
ambiguous and therefore produces a hold. Up to 15 hosts plus one transient
replacement can be observed; anything larger is rejected.

Scale-out recommendations are immediate. A lower desired count must remain
continuously valid for 15 minutes before `scale_in_window_elapsed` becomes
true. Any gap between accepted observations longer than 75 seconds resets that
window, as does leadership loss, a process restart, or invalid source data.
Even then the decision says that drain verification is required and
`mutation_allowed` remains false. The future mutating phase must still select
one host, stop placement, drain Nomad, prove zero allocations and workcells,
perform a targeted MIG deletion, and abort on timeout or ambiguous state.

## TAMS input contract

The HTTP source consumes the dedicated authenticated `GET /v1/ops/capacity`
envelope and strictly decodes this capacity object:

```json
{
  "generated_at": "2026-08-05T02:00:00Z",
  "capacity": {
    "revision": "opaque-unique-revision",
    "durable_sessions": 100,
    "active_workcells": 25,
    "parked_workcells": 2,
    "booting_workcells": 0,
    "draining_workcells": 0,
    "warm_target": 2,
    "counted_workcells": 27,
    "active_limit": 25,
    "queued_limit": 75,
    "planned_density_per_worker": 2,
    "hard_emergency_density_per_worker": 3,
    "desired_worker_hosts": 15,
    "worker_host_bounds": { "min": 2, "max": 15 },
    "fixed_control_plane_nodes": 6
  }
}
```

`generated_at` is the observation time. `capacity.revision` must be an opaque
unique revision for that capacity payload; reusing a revision with different
data is rejected. The controller also rejects a timestamp regression, a new
revision at the same timestamp, snapshots older than 90 seconds, and snapshots
more than 30 seconds in the future.

The dedicated TAMS capacity route and its workload-identity reader must be live
before this job is enabled. The route must validate the exact configured
audience and authorize only the dedicated API/controller service account for
this read-only snapshot. The fleet-wide `e2b-infra-instances` identity remains
attached to control, build, and worker VMs and is explicitly forbidden as an
allowlisted controller identity. Apply the foundation identity and grants,
perform the guarded API-pool replacement, and then bind both Terraform outputs
`api_controller_service_account_email` and
`api_controller_service_account_unique_id` into TAMS before activation. All
other service accounts, including same-project workers, must be rejected. This
is intentional rollout sequencing: enabling the job before both sides are
configured results only in fail-closed holds.

The canonical dev endpoint is
`https://api.tams.monad0.net/v1/ops/capacity`, with the exact Google ID-token
audience `https://api.tams.monad0.net`. The frontend origin is not an API
fallback: `https://tams.monad0.net/v1/ops/capacity` is invalid and must never
be configured.

All named capacity fields are required. Unknown capacity fields, negative
counts, invited-beta limit drift, density/bound drift, active plus draining
session workcells exceeding durable sessions or the 25-workcell beta cap,
active workcells exceeding the active limit, hard-emergency fleet overflow,
or disagreement
between TAMS' counted/desired values and the controller's recomputation are
rejected.

## Signals

Each accepted decision is emitted as a structured JSON log with the revision,
observation age, all workcell states, desired and actual hosts, direction,
scale-in window state, reason, and `mutation_allowed=false`.

The task exposes `/healthz` and Prometheus text metrics on port 9464. Signals
include leadership, snapshot validity, failure count, all capacity nouns,
desired/actual/draining hosts, low-demand duration, the scale-in window, and a
constant-zero mutation gauge. A follower clears its decision metrics rather
than serving a stale leader recommendation.

## Build and validation

```sh
go test -race ./packages/monad-worker-autoscaler/...
make build/monad-worker-autoscaler
make -C iac/provider-gcp keyless-runtime-check

cd iac/modules/job-monad-worker-autoscaler
terraform init -backend=false
terraform test

cd ../../../provider-gcp
terraform init -backend=false
terraform validate
```

Use the repository-pinned Terraform 1.7.5. `make
build-and-upload/monad-worker-autoscaler` uploads only the immutable
SHA-suffixed artifact used by the Nomad module; it does not update a mutable
alias.

## Identity and shadow rollout order

1. Apply the foundation plan that creates the dedicated attached identity and
   its scoped grants. Record the email and immutable numeric subject outputs.
2. Set the TAMS verifier's exact audience, GCP project, email, and numeric
   subject. A shared worker/build identity must remain rejected.
3. Read the live network-hardening marker before replacing the API pool. Use
   the existing guarded `api` stage only when the marker can advance
   `server -> api`, or when a previously reviewed `api` apply is eligible for
   its same-stage recovery path. Never reverse a marker that has reached
   `worker` or `build`. In that case, keep the observer disabled until a
   separately reviewed post-hardening API maintenance workflow can replace
   both API nodes without weakening the one-way rollout guard. In every case,
   prove load-balancer and Nomad convergence before activation.
4. Build and create-only upload `monad-worker-autoscaler.<infra-sha>`.
5. Set the five `MONAD_WORKER_AUTOSCALER_*` operator inputs and apply the
   ordinary workload plan with two shadow allocations. Mutation remains
   disabled.
6. Prove one elected leader reports a fresh accepted capacity revision, the
   follower publishes no stale decision, and no GCE resize or deletion occurs.
