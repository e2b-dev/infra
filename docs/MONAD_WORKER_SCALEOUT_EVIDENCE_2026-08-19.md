# Worker Scale-Out Live Evidence — 2026-08-19

The workload-aware worker controller (`monad-worker-autoscaler`) is live on
the dev fleet in **scale-out mode**: it polls the authenticated TAMS
`/v1/ops/capacity` contract, recomputes the invited-beta capacity formula,
and grows the `e2b-orch-client-rig` regional MIG toward the recommended host
count. It never shrinks the fleet; scale-in remains decision-only until a
typed drain owner exists on both sides of the contract.

## Design decision: contract-driven controller over the GCE CPU autoscaler

Both incidents that motivated this work (2026-08-18 test burst, 2026-08-19
warm-pool stampede) starved placements while the fleet was **nearly idle**:
fleet-average CPU stayed under 14.2% for the whole incident window and
5–10% during the 02:16–02:25Z starvation itself, while client-worker memory
idles at 72–86% "used" because of preallocated hugepages (see tams
`docs/ops/e2b-worker-capacity-2026-08-19.md`). A `cpu_target 0.7` GCE
autoscaler would never have fired; no valid `memory_target` clears the
hugepage noise floor. Placement refusals are *slot*-based, and only the
capacity contract sees slots. Terraform additionally makes the generic GCE
autoscaler and the workload-aware controller mutually exclusive on the MIG
(`worker-cluster/nodepool.tf` precondition), and the repo had already merged
the controller's shadow foundation. Scale-in is equally unsafe in both
designs (TAMS hardcodes `draining_workcells = 0`), so scale-out-only matches
`ONLY_SCALE_OUT` safety with correct semantics.

## What was found broken along the way

1. **The shadow observer was deployed but wedged.** Deployed 2026-08-14
   (tfstate serial 75, artifact `monad-worker-autoscaler.f2174680cac4`), its
   leader rejected every snapshot with `capacity durable_sessions exceeds
   the invited-beta limit of 100` (observed 03:05–03:06Z at 10s cadence):
   `durable_sessions` counts lifetime `project_sessions` rows and crossed
   100 in normal beta operation. Neither incident produced a shadow
   recommendation because input validation was dead. Fixed in #127 (the cap
   is removed; the 1M bounded-observation limit and all load-bearing
   envelope caps remain).
2. **The Terraform floor was frozen at the pre-incident topology.**
   `cluster_size == 2` guards (vs. live 4 after the 08-18 manual resize)
   plus a `client = 2` policy pin refused every workload plan. #126
   reconciled the floor at six hosts; #127 re-keyed the guards to an
   explicit `worker_host_floor` variable that must equal `cluster_size`.
3. **The prerequisite identity assert could never re-run** after its first
   apply (live state stores `b/`-prefixed bucket ids; fixed in #128).
4. **Ordinary workload plans are refused** while harvest #125's
   host-bootstrap script changes keep every instance template in pending
   replacement — correct behavior, but it blocked the job deploy. #129
   added the scoped `workload-controller-plan/apply` path (orchestrator-
   release precedent), with three follow-up fixes found live: a never-
   matching awk stop pattern in the release test (#129), missing fingerprint
   prune entries for the controller scratch dirs (#130), the assert scope
   arg and a `module.cluster` dependency edge that dragged the pending
   template replacement into the targeted plan (#131).

## Rollout timeline (all times UTC, 2026-08-19)

| Time | Event |
| --- | --- |
| 03:05 | Shadow observer confirmed wedged (`durable_sessions` rejections every 10s) |
| ~03:20 | PR #127 merged (`eedb9d4c4`): scale-out phase + durable_sessions fix |
| 03:22 | Baseline: MIG target 4, four ready Nomad workers |
| ~04:30 | Artifact `monad-worker-autoscaler.eedb9d4c4cd2` uploaded (create-only) |
| 04:35 | MIG resized 4→6 (`gcloud … resize --size 6`, reviewed floor of #126) |
| 04:55 | Six workers ready+eligible in Nomad |
| ~05:10 | IAM prerequisite applied: custom role `monadWorkerAutoscalerResize` (`compute.instanceGroupManagers.get/update` only) granted to `e2b-api-controller` — plan showed exactly 2 adds |
| 06:37 | Shadow phase applied via `workload-controller-apply` (controller job replaces the retired `-shadow` job). Leader accepts a fresh revision every 10s; follower silent; decisions sane (`active 0, booting 2, warm 2 → required 4 → desired 3 < actual 6 → scale_in hold, mutation_allowed=false`) |
| 06:45 | `MONAD_WORKER_AUTOSCALER_MODE=scale-out` applied (in-place job update). Leader logs `mode=scale-out`, still correctly holding at the floor under low demand |
| 06:46:04 | Burst dispatched: tams ke2e run 32224809144 — 14 concurrent session-provisioning flows (`RUN-1..8, FILE-1..6, --workers 14`) |
| 06:47:54 | **Resize 6→8** (`worker scale-out resize applied`, revision `xid8-1947574-…`) |
| 06:48:05 | **Resize 8→13** (revision `xid8-1947857-…`) |
| 06:48:14 | **Resize 13→14** (revision `xid8-1948182-…`; snapshot: active 5, booting 10, warm 11 → required 26 → desired 14) |
| 06:48:32 | ke2e run 32224809144 **completed: success** — all 14 concurrent flows green while the fleet was mid-scale-out |
| 06:49+ | Decisions stay `scale_out` (desired 13 ≤ MIG target 14): the targetSize-idempotency rail issues **no** duplicate resizes while booted hosts join Nomad |
| 06:50:00 | 13 workers ready+eligible in Nomad — the scaled-out capacity materialized ~2 minutes after actuation |

## Configuration of record (dev)

- Floor: 6 (`CLIENT_CLUSTERS_CONFIG cluster_size` = `MONAD_WORKER_AUTOSCALER_WORKER_FLOOR`), ceiling 15 (`worker_host_bounds.max`).
- Regional quota headroom measured for the full ceiling: CPUs 54/3000 used, instances 10/32, SSD ample (us-east4).
- Controller: 2 allocations on the API pool, Consul-elected leader, revision `eedb9d4c4cd2`, mode `scale-out`, mutation switch double-keyed (`MUTATION_ENABLED=scale-out-only` exact phrase).
- Identity: `e2b-api-controller@monad-code` — TAMS reads via GCE instance identity tokens (audience `https://api.tams.monad0.net`), GCE resize via metadata OAuth tokens under the custom role.
- Resize rails: target = max(desired, floor), never above 15, idempotent against MIG targetSize (not Nomad actuals), observed target > 16 refused as ambiguous, shrink impossible.

## Burst proof

All three resizes appear in the GCE operations log attributed to the
controller's identity — the audit trail distinguishes controller actuation
from the two earlier manual operator resizes:

```
TIMESTAMP                      STATUS  USER
2026-08-18T23:48:14.924-07:00  DONE    e2b-api-controller@monad-code.iam.gserviceaccount.com
2026-08-18T23:48:04.960-07:00  DONE    e2b-api-controller@monad-code.iam.gserviceaccount.com
2026-08-18T23:47:54.930-07:00  DONE    e2b-api-controller@monad-code.iam.gserviceaccount.com
2026-08-18T21:35:01.589-07:00  DONE    yasser@engram.org          (manual 4→6, floor)
2026-08-18T03:54:39.979-07:00  DONE    yasser@engram.org          (manual 2→4, incident)
```

Placement succeeded **during** the burst: the run's 14 concurrent
session-provisioning flows all passed (run 32224809144, 06:46:04→06:48:32Z),
whereas the same demand shape starved real users on the fixed 2-host fleet
on 08-18 and the 4-host fleet on 08-19 02:16Z.

Post-burst: demand decays, decisions flip to `scale_in` holds
(`mutation_allowed=false`, `requires_drain_verification=true`), and the
fleet **stays** at 14 hosts by design — shrinking requires the future drain
owner. Operational note: return to the floor manually only after the burst
sandboxes are gone (e.g. after a dev data reset), because a bare MIG resize
picks victims arbitrarily; prefer `delete-instances` on workers proven
empty, or resize after a reset has killed all sandboxes.

Follow-ups deliberately out of scope: the typed drain owner for scale-in
(both repos), retiring the one-time shadow-job-delete plan allowance after
this migration, and folding the controller path into the staged fleet
rollout once the harvest template replacement executes.
