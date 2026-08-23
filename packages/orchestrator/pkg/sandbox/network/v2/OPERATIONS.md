# Network v2 canary operations

Network v2 is an opt-in nftables datapath. The merge and deployment default
remains `NETWORK_VERSION=1`; enable v2 only on a canary node.

Dashboard and alert definitions live outside this repository. Deploy and
validate them before enabling a canary. The names below are OpenTelemetry
instrument names, not guaranteed Prometheus names: exporter normalization and
the backend's instance/node resource label must be verified on the canary.

## Enablement checklist

- Confirm privileged CI passed and the target has `CAP_NET_ADMIN` plus the v2
  kernel prerequisites.
- Deploy dashboards and alerts that query both metric namespaces.
- Drain and cordon the target node so it has no live sandboxes.
- Confirm startup reclaim is enabled and succeeds, then set
  `NETWORK_VERSION=2` and restart the orchestrator.
- Verify `orchestrator.network.datapath` reports value `1` with
  `network_version="2"`, grouped/joined by the verified instance or node
  resource label.
- Run synthetic DNS, outbound Internet, and proxy/firewall checks before
  returning the canary to service.

## Signals

Mixed rollouts must query both existing pool namespaces without renaming or
combining their series:

- v1: `orchestrator.network.slots_pool.*`
- v2: `orchestrator.network.v2.slots_pool.*`

New v2 operational instruments are:

- `orchestrator.network.v2.slots_pool.creation_failures`, with bounded
  `stage={acquire,create}`.
- `orchestrator.network.v2.host_firewall.mutation_failures`, with
  `operation={add_slot,remove_slot}`.
- `orchestrator.network.v2.host_firewall.reconciliation_failures`.
- `orchestrator.network.v2.host_firewall.connection_resets` and
  `orchestrator.network.v2.host_firewall.connection_reset_failures`, with
  `operation={add_slot,remove_slot,reconcile}`.
- `orchestrator.network.v2.host_firewall.reconciliation_skipped`, with
  `reason=unclean_startup_reclaim`, when reclaim ran but did not finish cleanly.

Also monitor `orchestrator.startup_reclaim.failed` with the exact attribute
`resource_type="network"`, and fatal logs including `v2 network prerequisites
not met`, `failed to create v2 host firewall`, `failed to reconcile v2 host
firewall slots`, and `failed to create network pool`.

Initial canary alert defaults, requiring calibration:

- v2 ready slots (`new` plus `reused`) equal zero for 2 minutes;
- any slot creation failure rate above zero for 5 minutes;
- any host-firewall connection-reset failure or reconciliation failure pages
  the canary owner;
- any startup network reclaim failure pages the canary owner.

## Rollout and abort criteria

Advance one drained node at a time. At every stage compare sandbox setup error
rate and latency with v1, synthetic DNS/outbound/proxy results, CPU, error-log
rate, pool availability, and all host-firewall failure counters.

Abort for a material setup regression, failed synthetic check, sustained CPU
or log-rate increase, zero ready slots, or any firewall reconciliation/reset
failure. Drain and cordon before rollback.

## Rollback

1. Drain and cordon the node. Never manually delete `v2-host-firewall` while
   live v2 slots remain; doing so cuts their connectivity.
2. Set `NETWORK_VERSION=1` and restart.
3. With startup reclaim enabled, the v1 startup path calls
   `PurgeHostFirewallTable` after reclaim. A purge failure is fatal because a
   stale v2 table can hijack v1 sandbox egress.
4. If the fatal purge cannot remove it and no live v2 slots remain, manually
   run `nft delete table inet v2-host-firewall`, then restart v1.

If `DISABLE_STARTUP_RECLAIM=true`, current code skips both startup reclaim and
the v1 `PurgeHostFirewallTable` rollback cleanup. Do not assume rollback is
safe in that mode: drain fully, assess and remove stale resources/table while
the service is stopped, or re-enable startup reclaim before restarting v1.
