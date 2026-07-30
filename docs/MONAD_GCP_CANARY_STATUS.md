# Monad GCP E2B canary status

Last verified: 2026-07-30 (Australia/Melbourne)

This is the current operator checkpoint for the GCP F1 lane at exact infra
revision `b20d6a57ca1ee2952d8d502cf52e1e228fa82493`.

## Live

- Project `monad-code`, region `us-east4`, workload zone `us-east4-c`.
- Three Nomad servers, one API node, one build worker, and one Firecracker
  client worker are running and stable.
- The live Nomad control plane has 11 jobs: API, client proxy, registry proxy,
  ingress, log collector, Loki job, orchestrator, two OTel collectors, Redis,
  and template manager.
- The operator lifecycle template is private and immutable at
  `monad-gcp-canary-base:infra-36c8d3cdd`.
- The dedicated synthetic canary team is empty after the completed benchmark.
- Cloud SQL is private, TLS-required, deletion-protected, and intentionally
  sized as the low-cost one-workcell control-plane database.
- ClickHouse and its GCE pool remain at target size zero. The dedicated GCE
  Loki pool also remains at zero; the current Loki job is placed on the API
  pool.

## F1 evidence completed

- Billing Budgets and Cloud Billing APIs are enabled.
- The project-scoped monthly alert is USD 5,000 with 50%, 75%-forecast, 90%,
  and 100% thresholds.
- Every instantiated runtime path uses the attached
  `e2b-infra-instances` service account through metadata ADC. All project
  service accounts have zero user-managed keys.
- All six hosts, all 11 Nomad jobs, all 45 retained Terraform state
  generations, current runtime artifacts, and the retained audit-log window
  were inspected for static Google credentials.
- Live metadata-ADC calls to GCS, IAM Credentials `signBlob`, the GCR
  credential helper, and Artifact Registry passed from both API and
  Firecracker worker roles.
- The `n1-standard-8` worker benchmark completed levels one through five under
  CPU, memory, fsynced disk, proxied network, and noisy-neighbour pressure.
- Cleanup returned the API inventory and Firecracker process count to zero,
  restored all hugepages, and left swap unused.

Full methods, caveats, measurements, and the supported autoscaling envelope
are in [`MONAD_F1_LIVE_EVIDENCE_2026-07-30.md`](MONAD_F1_LIVE_EVIDENCE_2026-07-30.md).

## Supported worker envelope

- Plan at two active 2-vCPU workcells per ready `n1-standard-8` worker.
- Enforce three active workcells per worker as the hard placement cap.
- Four workcells on one worker is unsupported: it saturated CPU and produced a
  916 ms command-latency outlier.
- The current fleet has one worker and no GCE autoscaler.
- With one-worker replacement reserve, current SSD quota permits two total
  workers: four planned or six hard-cap active workcells.
- Durable paused sessions are not active-workcell slots. This benchmark does
  not publish a numeric durability/SLO ceiling; resume admission must respect
  the active envelope.

## Remaining deployment work

1. Review and merge this evidence/benchmark change.
2. In a separate saved-plan change, configure the client-pool autoscaler for
   minimum one and maximum two workers at the reviewed CPU target, then prove
   scale-out, scale-in, replacement reserve, and post-apply readiness.
3. Obtain the requested regional SSD quota increase before raising the worker
   ceiling beyond two. Re-read all live quotas in the reviewed plan.
4. Bring up and runtime-test the zero-sized ClickHouse backup path before
   claiming that data plane as beta-ready.
5. Keep TAMS beta routing off until the Monad runtime template plan is merged,
   pulled, built, and verified against the template-identity/create-ref
   decision.

Do not resize the MIG directly. Workload changes remain governed by the
repository's exact saved plan, provenance manifest, mutation lease, topology
and quota guards, and post-apply readiness checks.
