# Monad GCP network hardening status and rollout

Status date: 2026-08-06. GCP project: `monad-code`. Region: `us-east4`.

This document records the read-only live audit behind the invited-beta network hardening and the
prerequisites for applying it. The changes described here are source changes only until a guarded,
reviewed Terraform plan is applied.

## Live audit

The audit used the authenticated `yasser@engram.org` gcloud account and printed no credential or
SSH-key value.

- All eight `e2b-orch-*` instances (three servers, two API nodes, two workers, and one build node)
  have private `10.150.0.0/20` addresses and no per-instance public address.
- The first guarded `network` apply enabled decision logging but the provider/API result retained
  both `0.0.0.0/0` and `35.235.240.0/20` on
  `e2b-orch-internal-remote-connection-firewall-ingress` at priority 900. The priority-1000
  public deny therefore still never sees those packets. The convergence sentinel rejected that
  effective state, kept the stage marker at `disabled`, and preserved the rollout lease for a
  same-stage recovery. The saved applied plan expected the replacement, but Google provider
  v6.50.0's source-range diff suppression preserves the API's default `0.0.0.0/0` when one sole
  source range is replaced by another. The live union is therefore provider behavior that the
  priority-700/800 overlay must safely shadow, not evidence that the reviewed plan omitted IAP.
  The bounded recovery installed and proved that overlay; the persisted live stage is now
  `network`.
- Project common metadata contains a legacy `ssh-keys` entry and no `enable-oslogin` entry. None of
  the eight live fleet instances has an instance-level `enable-oslogin` value. The effective
  `constraints/compute.requireOsLogin` response did not report enforcement.
- The project IAM policy has no project-local `roles/compute.osLogin`,
  `roles/compute.osAdminLogin`, or `roles/iap.tunnelResourceAccessor` binding. Inherited IAM was not
  proven by this query and must not be assumed. The active operator account's OS Login profile
  returned no POSIX account and no OS Login SSH key.
- The live workload subnet is `10.150.0.0/20`; fleet nodes are `10.150.0.x`. The private Cloud SQL
  addresses are `10.26.7.3` and `10.26.7.6`. These destinations, metadata
  `169.254.169.254`, RFC1918, CGNAT, loopback, and IPv6 local ranges all fall within the
  Firecracker slot's existing predefined nftables deny set.
- The GCP environment does not set `ALLOW_SANDBOX_INTERNAL_CIDRS`; its effective value is the empty
  default. This branch makes any non-empty GCP value a Terraform validation error and prevents the
  generic orchestrator environment map from overriding either that value or the fixed
  `SANDBOX_ORCHESTRATOR_IP=192.0.2.1` host-proxy address.

## Source invariants

- Dev installs a new exact IAP-only allow for `35.235.240.0/20`, ports 22/3389, at priority 700.
  The public-source deny moves to priority 800. The provider-retained compatibility allow remains
  explicit at priority 900 and is therefore shadowed for every non-IAP source. All three dev rules
  log decisions with `EXCLUDE_ALL_METADATA`, and both saved-plan and live-convergence guards prove
  their exact sources, ports, actions, logging, target tag, and precedence.
- Terraform orders the transition as exact IAP allow, then higher-precedence public deny, then any
  legacy-rule broadening. An interrupted apply therefore cannot strand IAP behind the deny or
  expose a broad legacy allow before that deny exists.
- Outside dev the existing IAP-only allow remains priority 900 and retains its prior logging shape;
  the public deny remains priority 1000. The overlay is dev-only, so this recovery cannot introduce
  otherwise-unapplyable staging/production drift.
- Server, API, worker/build, Loki, and ClickHouse instance templates add
  `enable-oslogin = "TRUE"` only when their state-backed serial stage has been reached. The API
  template no longer ignores all metadata changes.
- `os_login_operator_access_confirmed` defaults to false. Its precondition is inside
  `module.cluster`; every template path and all three dev administrative firewalls consume that in-graph
  guard. The transient complete-module topology audit and every exact saved stage plan therefore
  include it. Worker and build templates consume the guard only in their resource lifecycle
  precondition: a module-wide
  dependency would defer their `google_compute_image` reads while the guard changes and spuriously
  plan template/MIG replacement during the network-only stage.
- The staged replacement path is restricted to the current `dev` invited-beta fleet. Any non-dev
  stage fails at the in-module plan guard, leaving its upstream opportunistic MIG policy unchanged;
  production/staging retain their existing IAP-only firewall posture at `disabled` and require a
  separately reviewed instance-replacement strategy. The ordinary workload-plan guard is
  environment-aware: it permits non-dev plans to initialize or preserve only the ledger root at
  `disabled`, while still rejecting any non-dev stage advancement or regression.
- The cumulative rollout ledger permits exactly
  `disabled -> network -> server -> api -> worker -> build`. Every completed stage has its own
  completion/marker pair. A current-stage convergence sentinel depends on the exact current pool
  and the previous marker, then waits for the administrative firewalls and affected MIGs
  to report both `status.isStable` and `status.versionTarget.isReached`, inventories their exact
  instance IDs and target templates, proves IAP/OS Login against each ID, and binds the replacement
  names to healthy Nomad quorum/client state before that marker advances. Server/API stages also
  require healthy load-balancer membership for that same inventory.
  The saved plan contains the cumulative prior/current chain but excludes future pools. Its
  assertion permits only the current stage's exact firewall or pool boundary (including a subset
  left by a partial apply), requires all prior resources and markers to remain no-ops, rejects all
  other drift, and retains the existing topology, quota, worker-MIG, and generic-autoscaler
  ownership checks. A separate mode-0600 complete-module plan is used only for topology/quota
  audit; it is never published or applied and is deleted before mutation starts.
- The replacement access probe explicitly permits an SSH host-key rotation because a managed
  instance repair or image replacement can regenerate the guest key before the operator's local
  `google_compute_known_hosts` entry changes. This exception is confined to the IAP tunnel. The
  remote probe must still return the exact GCE instance ID from the metadata server and confirm
  `enable-oslogin=TRUE`; a connection to any other instance or a non-OS-Login guest fails closed.
- Every stage requires a mode-0600, non-symlink checkpoint bound to the exact project, region,
  zone, prefix, Git head, named operator, and a maximum one-hour validity window. Its checks and
  evidence are stage-specific. The complete checkpoint and digest are embedded in the saved-plan
  manifest; its full schema and expiry are revalidated under the shared rollout lease immediately
  before Terraform mutation.
- Guest private/control-plane denies run on the tap before host NAT and before tenant allow rules.
  The host's own metadata ADC path does not traverse that tap rule.
- `make -C iac/provider-gcp network-security-check` guards all of these source relationships and
  runs the root-free GCP control-plane CIDR test.

## Validation evidence

The local branch passed the following checks before handoff:

- Terraform 1.7.5 formatting and `validate` for both `iac/provider-gcp` and the Packer network
  configuration.
- The complete workload plan-assertion fixture suite, including quota, mutation-lease, release,
  cluster-readiness, template-manager, and worker-startup guards.
- `network-security-check`, including real Terraform target-closure plans for all five stages that
  prove the cumulative prior/current graph, future-pool exclusion, precise child-output edges, the
  initial counted-sentinel replacement behavior, and the closed in-graph OS Login guard.
- Serial rollout fixtures for every allowed transition, exact mutation boundaries, fresh
  checkpoints, post-plan checkpoint expiry rejection, asynchronous MIG convergence, partial-stage
  retry, reverse-stage rejection, skipped-stage rejection, closed-guard rejection, and protection
  of generic-autoscaler ownership.
- The complete Linux `packages/orchestrator/pkg/sandbox/network` suite in a privileged container,
  including real network-namespace, iptables, and nftables tests.
- The complete Linux `packages/orchestrator/pkg/tcpfirewall` suite with its Docker-backed listener
  test, plus the shared sandbox-network package tests.
- The keyless-runtime guard suite, `shellcheck`, `actionlint`, and `git diff --check`.

## Apply prerequisites

Do not apply this branch merely because validation is green.

1. Choose the named operator group or principal. Grant the least-privilege project access needed
   for `roles/iap.tunnelResourceAccessor` and `roles/compute.osAdminLogin`; verify inherited access
   explicitly if inheritance is intentional.
2. Prove an IAP TCP tunnel and OS Login administrative SSH on a disposable instance built from an
   OS-Login-enabled candidate template. Do not use a production worker as the first proof.
3. Set `OS_LOGIN_OPERATOR_ACCESS_CONFIRMED=true` in the ignored selected environment only after
   that evidence exists. The default false value is intentional.
4. Execute only the ordered stages `network`, `server`, `api`, `worker`, `build`. Before each one,
   create a fresh mode-0600 checkpoint matching the schema enforced by
   `scripts/assert-network-hardening-checkpoint.sh`. Record concrete evidence identifiers or
   command-result locations, not bare assertions.

   All checkpoints contain `schema_version`, `stage`, `gcp_project_id`, `gcp_region`, `gcp_zone`,
   `prefix`, `source_git_head`, `operator_principal`, `observed_unix`, `expires_unix`, and exact
   `checks`/`evidence` maps. Every check is `true`; every same-named evidence value is a non-empty
   command result, log query, or inventory URI. `network` requires `control_plane_healthy`,
   `iap_tunnel_access`, and `os_login_admin_access`. `server` and `api` require the two access
   checks plus `nomad_quorum_healthy`, `api_load_balancer_healthy`, and `target_pool_healthy`.
   `worker` requires the access checks plus `target_pool_drained`, `zero_target_allocations`,
   `zero_target_workcells`, and `durable_sessions_preserved`. `build` replaces the final item with
   `build_queue_quiesced`.
5. Follow the existing drain procedure before the worker/build stages: halt placement,
   pause or snapshot workcells, prove durable upload, drain Nomad, and verify zero allocations.
   The server pool must retain its reviewed one-surge, explicit-`SUBSTITUTE`, zero-unavailable,
   120-second-ready policy. Port 50001 `/healthz` must identify that exact GCE instance as an alive,
   healthy Nomad voter while Autopilot has three healthy voters and failure tolerance at least one;
   the stock leader-reachable agent endpoint is insufficient. The MIG must retain
   `on_failed_health_check=DO_NOTHING` so a quorum-wide unhealthy signal gates the Updater but never
   auto-repairs all remaining voters; infrastructure failures retain non-forced `REPAIR`. This
   policy's normalized health-check resource ID must exactly match `server_nomad_check`; unknown
   identity or a different health check is a rollout-stopping plan error. This brings a fourth
   proven voter up before removing an established member. Roll server and API pools
   without losing quorum or load-balancer health.
   Port 50001 is reachable only through the existing load-balancer health rule from
   `130.211.0.0/22` and `35.191.0.0/16` to the `orch` target tag; a Terraform precondition and
   static regression test bind the voter endpoint to that narrow rule. The live
   `e2b-load-balancer-hc` rule was rechecked with that exact shape before this change was published.
6. Create, inspect, and apply one stage at a time (example for `server`):

   ```bash
   mise exec -- make -C iac/provider-gcp workload-cluster-plan \
     WORKLOAD_CLUSTER_STAGE=server \
     WORKLOAD_CLUSTER_CHECKPOINT=/private/path/server-checkpoint.json
   mise exec -- terraform -chdir=iac/provider-gcp \
     show .tfplan.workload-cluster.server.dev
   mise exec -- make -C iac/provider-gcp workload-cluster-apply \
     WORKLOAD_CLUSTER_STAGE=server \
     WORKLOAD_CLUSTER_CHECKPOINT=/private/path/server-checkpoint.json \
     CONFIRM='APPLY NETWORK HARDENING server'
   ```

   The reviewed plan must contain only the exact current-stage mutation plus the in-module guard
   and cumulative prior/current completion/marker ledger. The cluster plan forces replacement of
   the current convergence sentinel on every attempt, including a marker-only retry, so the apply
   graph re-proves the live fleet before
   the state marker can advance. The shared rollout lease remains held until the affected MIG is
   stable, has reached its target version, and its identity-bound post-replacement access and service
   evidence passes. Never reuse a checkpoint or plan for another stage.

   If a later reviewed change strengthens a pool whose stage marker is already
   complete, re-enter only that same stage with
   `WORKLOAD_CLUSTER_REFRESH_STAGE=<stage>` on both the plan and apply commands.
   A planned refresh uses the normal shared rollout lease and a fresh
   exact-head checkpoint; it cannot borrow a recovery lease. The persisted
   stage marker must remain a no-op and the existing convergence sentinel must
   be force-replaced, so this mode cannot recreate a missing sentinel, skip a
   stage, or mutate outside the stage's existing exact resource boundary. The
   refresh value is recorded in the saved-plan provenance. For example:

   ```bash
   mise exec -- make -C iac/provider-gcp workload-cluster-plan \
     WORKLOAD_CLUSTER_STAGE=server \
     WORKLOAD_CLUSTER_REFRESH_STAGE=server \
     WORKLOAD_CLUSTER_CHECKPOINT=/private/path/server-refresh-checkpoint.json
   mise exec -- make -C iac/provider-gcp workload-cluster-apply \
     WORKLOAD_CLUSTER_STAGE=server \
     WORKLOAD_CLUSTER_REFRESH_STAGE=server \
     WORKLOAD_CLUSTER_CHECKPOINT=/private/path/server-refresh-checkpoint.json \
     CONFIRM='APPLY NETWORK HARDENING server'
   ```

   A failure before the current waiter succeeds leaves the persisted marker at the prior stage; fix
   the in-boundary cause. A failure after the marker is created, including a dirty post-apply plan,
   may leave the current marker and preserved lease. In that case, the bounded retry accepts only a
   no-op current-stage marker while the forced sentinel replacement
   re-proves convergence. If an interrupted initial transition or forced retry leaves the sentinel
   absent, only a plan carrying the validated, generation-bound recovery token for that exact stage
   may recreate it; ordinary, mismatched-stage, skipped-stage, and next-stage plans remain blocked.
   Once Terraform apply has
   started, any timeout, interruption, or unverifiable post-apply result preserves the
   generation-bound shared lease and its private recovery directory.
   Prove the original process is no longer running. If recovery continues at the same exact Git
   head, create a fresh checkpoint for the same stage and run:

   ```bash
   mise exec -- make -C iac/provider-gcp workload-cluster-recover-lease \
     WORKLOAD_CLUSTER_STAGE=<stage> \
     WORKLOAD_CLUSTER_CHECKPOINT=<checkpoint> \
     WORKLOAD_CLUSTER_RECOVERY_TOKEN=<preserved-token> \
     CONFIRM='RELEASE NETWORK HARDENING LEASE <stage>'
   ```

   A recovery lease cannot be copied to a repaired checkout: its holder is intentionally bound to
   the exact source commit that began the failed apply. When the in-boundary repair has instead
   landed as a reviewed descendant, use a fresh checkpoint bound to that descendant and transfer
   the continuously held generation with the dedicated network-only command:

   ```bash
   mise exec -- make -C iac/provider-gcp workload-cluster-rebind-recovery-source \
     WORKLOAD_CLUSTER_STAGE=network \
     WORKLOAD_CLUSTER_CHECKPOINT=<fresh-descendant-checkpoint> \
     WORKLOAD_CLUSTER_RECOVERY_TOKEN=<preserved-original-token> \
     WORKLOAD_CLUSTER_REBOUND_TOKEN=<new-private-token-path> \
     CONFIRM='REBIND NETWORK HARDENING LEASE network'
   ```

   The command requires a clean exact descendant, permits only the reviewed firewall repair,
   guard, test, and runbook paths between the two commits, and atomically replaces the canonical
   lease object under its old generation. There is no unlocked interval. The new mode-0600 token
   captures the replacement generation and exact descendant holder; only after that succeeds is
   the original token removed. Any unrelated diff, stale generation, wrong scope, existing output
   path, or non-network stage fails before mutation. Use the rebound token for the bounded plan and
   apply below. If either the object-transfer response or replacement-token capture cannot be
   proven, stop and inspect the canonical object; never delete or reacquire it speculatively.

   The recovery command re-proves live firewall/MIG convergence, exact replacement identity,
   post-replacement IAP/OS Login and stage-specific Nomad/load-balancer health, then requires a clean
   scoped Terraform post-plan before releasing that exact lease. If Terraform still has same-stage drift,
   it leaves the lease held. Use the same token to create and review the bounded retry plan, then
   apply it under the still-held lease:

   ```bash
   mise exec -- make -C iac/provider-gcp workload-cluster-plan \
     WORKLOAD_CLUSTER_STAGE=<stage> \
     WORKLOAD_CLUSTER_CHECKPOINT=<checkpoint> \
     WORKLOAD_CLUSTER_RECOVERY_TOKEN=<preserved-token>
   mise exec -- make -C iac/provider-gcp workload-cluster-apply \
     WORKLOAD_CLUSTER_STAGE=<stage> \
     WORKLOAD_CLUSTER_CHECKPOINT=<checkpoint> \
     WORKLOAD_CLUSTER_RECOVERY_TOKEN=<preserved-token> \
     CONFIRM='APPLY NETWORK HARDENING <stage>'
   ```

   The apply releases the borrowed token only after the normal live-convergence and clean-post-plan
   proofs pass. Borrowed planning, recovery, and every apply first prove that the canonical GCS
   lease object still matches the private token's exact scope, generation, and holder. The live-bound
   holder names the selected environment and exact Terraform backend prefix, so a token copied from
   another environment cannot recover or release this state. Stale, replaced, or wrong-scope token
   copies fail before mutation. Reverse-stage plans remain fail-closed; do not bypass the workflow
   for an ad hoc rollback.
7. The sentinel enforces replacement-identity-bound IAP/OS Login and role-specific service health.
   Before proceeding, separately retain the broader canary evidence for attached-service-account ADC,
   host metadata reachability, guest metadata/private-control-plane denial, public egress through
   Cloud NAT, and zero leaked workcells.
8. Persist the successfully applied stage as `NETWORK_HARDENING_ROLLOUT_STAGE` in the ignored
   selected environment. Only after every fleet node is on the `build` stage should the legacy project
   `ssh-keys` metadata be removed in a separate reviewed operation with a rollback principal
   already proven.

The ordinary full `workload-plan` and `workload-apply` paths never initialize or change this
stage. They require the exact cumulative completion/marker ledger through the selected stage to be
non-disabled no-ops, with no missing, duplicate, legacy, or future ledger resource in the reviewed
plan. Any disabled, forward, skipped, reverse, or mismatched stage is rejected before publication
and again before apply. Use only the cluster workflow above for a
stage transition, and persist the proven stage in the selected environment before returning to
ordinary workload changes.

The repository does not grant operator IAM because the authoritative operator group is an external
ownership decision. Until that principal is named and the canary proof passes, the Terraform guard
must remain closed and this change must remain unapplied.
