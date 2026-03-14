# E2B Network v2 — Implementation Summary

**Date:** 2026-03-13
**Status:** PoC complete, dual-node verified, migration implemented
**Audience:** Engineering team

---

## What we built

A complete replacement of the per-sandbox iptables networking stack with an nftables-based architecture that achieves O(1) rule operations, cross-node migration, and egress identity — all within the existing Firecracker TAP model.

### By the numbers

| Metric | v1 | v2 |
|--------|----|----|
| iptables calls per sandbox create/destroy | 11 | **0** |
| Host firewall rule count vs sandbox count | O(N) — linear chain growth | **O(1)** — nftables sets |
| Kernel firewall mechanism | iptables | **nftables** |
| Migration support | None | **Snapshot/restore + WireGuard transfer** |
| Egress identity | Host IP masquerade | **Per-profile sticky IP via gateway** |

**Codebase**: ~2,800 lines implementation + ~1,300 lines tests across 22 files in `packages/orchestrator/internal/sandbox/network/v2/`.

---

## How it works

### Host networking (per sandbox)

The per-sandbox topology is unchanged from v1 — we kept what works:

```
Guest eth0
   |
Firecracker virtio-net
   |
 tap0  (inside sandbox netns)
   |
 nftables SNAT/DNAT          ← v2: replaces iptables NAT
   |
veth-sbx -------- veth-host
                     |
                 nftables sets    ← v2: replaces iptables per-sandbox rules
                     |
                 host uplink
```

Each sandbox gets its own network namespace. No shared bridge, no shared L2 between tenants. The key difference: instead of adding/removing iptables rules per sandbox, v2 adds/removes **set elements** in a constant-size nftables table.

### The host firewall (`v2-host-firewall` table)

One table, created once per orchestrator process. Contains:

- **`v2_veths` set** — all active veth interface names (hash lookup)
- **`v2_host_cidrs` set** — all active host CIDRs (interval set)
- **`forward` chain** — policy DROP, allows veth↔gateway traffic
- **`prerouting` chain** — service redirects (hyperloop, NFS, portmapper) + TCP firewall proxy redirects
- **`postrouting` chain** — masquerade for sandbox traffic

Adding a sandbox = 2 set element inserts + 1 `nft flush`. Removing = 2 set element deletes + 1 flush. Rule count stays constant at ~10 regardless of whether you have 1 or 1000 sandboxes.

### In-namespace NAT

Each namespace has an nftables table (`slot-firewall`) with:
- **POSTROUTING**: SNAT namespace IP → host IP (outbound)
- **PREROUTING**: DNAT host IP → namespace IP (inbound)

Same kernel conntrack as iptables, so `SO_ORIGINAL_DST` in the TCP firewall proxy works unchanged.

### Slot pooling

`V2Pool` implements the same `PoolInterface` as v1 — drop-in replacement. Two buffered channels:
- `newSlots` — continuously pre-created by background goroutine
- `reusedSlots` — returned from stopped sandboxes

Sandbox creation is a non-blocking channel receive, not a synchronous namespace+veth+tap creation.

### Version gating

Controlled by `NETWORK_VERSION` env var in `main.go`:
```go
if config.NetworkConfig.NetworkVersion == 2 {
    // v2 path: nftables host firewall + V2Pool
} else {
    // v1 path: iptables + old Pool
}
```

Both pools satisfy `PoolInterface`. The rest of the sandbox pipeline (Firecracker, envd, storage) is unchanged.

---

## Egress identity

### The problem

v1: all sandboxes masquerade through the host's public IP. If a sandbox migrates to another host, its outbound IP changes. Customers needing stable IPs for allowlisting are stuck.

### The solution

```
Sandbox → veth (fwmark) → policy route → WireGuard tunnel → Egress gateway → SNAT → Internet
```

Each sandbox is assigned an **EgressProfile** that determines its outbound identity:

```
EgressProfile {
  mode:         "customer-shared" | "sandbox-dedicated"
  backend_type: "gateway" | "cloud_nat" | "direct"
  public_ip_set: [sticky IPs]
  gateway_addr:  10.99.0.1  (WireGuard IP of gateway)
  fwmark:        0x300
  route_table:   300
}
```

**How fwmark routing works:**
1. nftables `mangle_prerouting` chain marks packets from the sandbox's veth with the profile's fwmark
2. `ip rule add fwmark 0x300 lookup 300` matches the mark
3. Table 300 has `default via 10.99.0.1 dev wg0` — traffic enters WireGuard
4. On the gateway node, nftables SNATs to the sticky public IP

**Key finding from PoC**: `net.ipv4.conf.all.src_valid_mark=1` is required or the kernel won't reroute forwarded packets based on fwmark.

### What's implemented

- `EgressProfile` + `EgressManager` — data model and lifecycle
- `SetupPolicyRoute` / `TeardownPolicyRoute` — ip rule + route table
- `SetupFwmarkInNftables` — per-veth fwmark marking in host firewall
- `SetupEgressGateway` / `TeardownEgressGateway` — gateway-side nftables (forward + SNAT)
- WireGuard tunnel setup/teardown via `wgctrl`

### What's NOT implemented yet

**This is the main outstanding work for production:**

1. **Cloud NAT backend** — GCP can't do many public IPs per NIC. Need Cloud NAT with source-based rules (still Preview) or self-managed gateway nodes. AWS/Azure are cleaner (secondary private IPs + EIPs).

2. **EgressProfile lifecycle in control plane** — currently profiles are created in-process. Production needs: API for CRUD, persistent storage, assignment to customers/sandboxes, billing integration.

3. **Gateway HA** — current PoC has a single gateway (box). Production needs failover: AWS uses EIP movement, GCP uses Cloud NAT or gateway replacement, on-prem uses VRRP.

4. **Per-profile SNAT on gateway** — the gateway currently SNATs all traffic to one IP. Need per-source-CIDR or per-fwmark SNAT rules so different customers get different public IPs.

5. **TCP firewall proxy egress awareness** — the proxy currently intercepts ALL TCP. For per-profile egress (where some customers go through the gateway and others don't), the proxy needs to be aware of egress profiles.

---

## Cross-node migration

### The flow

```
Source (w1)                               Target (box)
═══════════                               ═══════════

1. Validate preconditions
   (arch, FC version, kernel, CPU)
2. Capture sandbox config via List()
3. Check storage mobility (no volumes)
4. Run PreMigrateHook (drain)
5. Pause VM → create snapshot
6. rsync snapshot files ──────────────→  7. Receive into template cache
   (via WireGuard tunnel)
                                         8. Create(Snapshot=true)
                                            new ExecutionId
                                            → restore VM from snapshot
9. [Optional] IP forwarding:
   ip route add <old>/32
     via <targetWgIP> dev wg0          10. DNAT <old> → <new slot IP>

                                         11. Sandbox running on target
```

**Downtime window**: steps 5–8 (pause → resume). PoC target: < 10 seconds for minimal sandboxes.

### What's implemented

- `MigrationCoordinator` — connects to both orchestrators via gRPC, orchestrates the full flow
- `MigrationPreconditions` — validates arch, FC version, kernel, CPU family, guest ABI compatibility
- Config capture from source via `List()` + `proto.Clone` — target gets correct VM params
- Storage mobility check — rejects sandboxes with volume mounts
- `PreMigrateHook` callback — caller can drain connections before pause
- New `ExecutionId` on target — prevents stale routing
- `BuildID` validation — prevents path traversal in rsync args
- IP forwarding via WireGuard — `SetupIPForward` (source route) + `SetupMigrationDNAT` (target DNAT)
- Surgical rule removal — tracks nftables rule handles for per-IP teardown
- Idempotent DNAT setup — duplicate calls replace rather than leak
- Demo script (`cmd/migrate-demo/`) with step-by-step timing output

### What's NOT implemented yet

1. **Firecracker `network_overrides`** — if the target slot has a different TAP name, the restored VM won't find its NIC. Needs proto field in `SandboxConfig`. Currently works because both nodes use the same naming.

2. **UFFD lazy memory load** — current transfer is full snapshot rsync. Production should use UFFD to start the VM immediately and page-fault in memory on demand.

3. **Edge/egress cutover** — IP forwarding via WireGuard is a PoC stopgap. Production should update the `sandbox_id → host_id` mapping in the control plane so edge/egress services route to the new host directly. No WireGuard hop needed.

4. **Iterative dirty-page streaming** — current pause is a hard stop. Production could do pre-copy (stream dirty pages while VM runs) to minimize the final pause window.

5. **Control plane integration** — marking sandbox as MIGRATING, updating routing catalog, notifying edge services, handling rollback on failure.

---

## WireGuard host mesh

Single `wg0` interface per node. Currently connects box (10.99.0.1) and w1 (10.99.0.2) on port 51820.

Used for:
- Migration snapshot transfer (rsync over SSH through tunnel)
- Egress gateway traffic (policy-routed sandbox traffic)
- Migration IP forwarding (old host IP → target via tunnel)

**Not** used for per-sandbox peer churn — WireGuard peers are hosts only.

---

## What works well (the good)

1. **Zero iptables, O(1) nftables** — verified on both nodes. `iptables -L -n` shows zero veth references. Host firewall rule count is constant.

2. **Drop-in replacement** — `PoolInterface` means the rest of the stack (API, Firecracker, envd, storage) doesn't know or care about v2. Flip `NETWORK_VERSION=2` and everything works.

3. **Same kernel primitives** — no new kernel modules, no eBPF for security, no userspace datapath. nftables + netns + netlink = well-understood, debuggable with standard tools.

4. **Egress separation works** — fwmark → policy route → WireGuard → SNAT. Verified that sandbox traffic exits through the gateway with a different public IP than the host.

5. **Migration plumbing is complete** — pause, transfer, resume, IP forwarding, surgical teardown. The coordinator handles config capture, precondition checks, storage validation, execution ID rotation.

6. **Comprehensive tests** — 43 tests covering nftables operations, concurrent access, idempotency, surgical rule removal, precondition validation, build ID injection prevention.

## What needs work (the bad)

1. **rsync for snapshot transfer** — migration PoC shells out to rsync over SSH. Works for the PoC but production needs the existing gRPC ChunkService (peer-to-peer, no SSH dependency, parallel chunks).

2. **IP forwarding bypasses TCP firewall proxy** — migration-forwarded traffic arriving on wg0 doesn't go through the proxy. Documented and scoped (only for specific migrated IPs, temporary), but a security gap. Production replaces this with edge/egress cutover.

3. **Single egress gateway** — no HA, no per-customer SNAT differentiation, no Cloud NAT integration.

4. **PublishProfile is a stub** — data model only. Published ports still use the existing proxy architecture. The design doc's `PublishProfile` with edge/ingress cutover is future work.

### Scaffolding (intentionally deferred, not gaps)

- **eBPF observability** — `VethObserver` is wired into the pool and network lifecycle (`Attach`/`Detach`/`ReadCounters` called at the right points). When someone writes the actual BPF program for per-sandbox counters and flow export (design §4.6), they fill in those methods — no plumbing changes needed. Counters return zeros until then.

- **UFFD lazy memory** — the migration PoC does a full snapshot rsync before resume. Production would use UFFD to start the VM instantly and page-fault memory in on demand. This is a migration optimization, not a networking gap.

---

## Outstanding work: external IPs (egress identity)

This is the highest-priority production gap. Current state and what's needed:

### Current state (PoC)
```
Sandbox → fwmark → policy route → wg0 → gateway box → SNAT to host IP → Internet
```
Works for proving the routing. All sandboxes through the gateway get the gateway's public IP.

### Production requirements

| Requirement | Status | Work needed |
|---|---|---|
| Per-customer sticky IP | Architecture exists | Gateway SNAT rules per customer, IP pool management |
| Per-sandbox dedicated IP | Architecture exists | 1:1 SNAT rule + IP allocation |
| Cloud NAT (GCP) | Not started | GCP Cloud NAT API integration, source-based rules |
| AWS EIP binding | Not started | ENI secondary IP + EIP association API |
| Azure public IP config | Not started | NIC IP configuration API |
| Gateway HA | Not started | Provider-specific failover (EIP move, Cloud NAT, VRRP) |
| IP pool management | Not started | Allocation, reservation, billing, reclamation |
| Migration preserves egress | Architecture exists | During migration, EgressProfile stays the same; gateway keeps the SNAT rule |
| TCP proxy egress awareness | Not started | Proxy must check EgressProfile before allowing connections |
| Customer API | Not started | CRUD for EgressProfile, IP assignment, status |

### Recommended next steps (priority order)

1. **GCP Cloud NAT integration** — most E2B traffic is on GCP. Use Cloud NAT with manual IP pools for coarse customer grouping. Evaluate source-based NAT rules (Preview) for per-customer granularity.

2. **Per-customer SNAT on self-managed gateways** — for customers who need a specific IP immediately. Extend `EgressGatewayConfig.SNATRules` to support per-fwmark SNAT (the data model already has `FwMark` on `SNATRule`).

3. **Gateway HA** — for GCP: Cloud NAT handles this natively. For self-managed gateways: active-passive pair with health checks and IP failover.

4. **TCP proxy egress awareness** — the proxy currently forwards all TCP. It needs to check the sandbox's EgressProfile to decide whether to allow/block based on egress-specific domain lists.

5. **Customer-facing API** — EgressProfile CRUD, IP status, migration compatibility checks.

---

## Deployment topology (PoC lab)

```
                    Internet
                       |
              ┌────────┴────────┐
              │  box             │
              │  192.168.100.137 │
              │  wg0: 10.99.0.1 │
              │                  │
              │  v2 orchestrator │
              │  API (port 80)   │
              │  egress gateway  │
              └────────┬────────┘
                   wg0 │ (WireGuard, port 51820)
              ┌────────┴────────┐
              │  w1              │
              │  192.168.100.253 │
              │  wg0: 10.99.0.2 │
              │                  │
              │  v2 orchestrator │
              └─────────────────┘
```

Both nodes run v2 orchestrators. API on box load-balances sandbox creation across both nodes. WireGuard tunnel carries egress traffic and migration data.

---

## Test results

| Test suite | Count | Status |
|---|---|---|
| Pure unit tests (preconditions, validation, state) | 6 | Pass (any platform) |
| nftables operations (host firewall, DNAT, teardown) | 12 | Pass (root + Linux) |
| Network lifecycle (full create/destroy, no iptables) | 3 | Pass (root + Linux) |
| Egress (profiles, gateway, policy routing) | 9 | Pass (root + Linux) |
| Slot pooling and registry | 5 | Pass (any platform) |
| IP forwarding (route + DNAT + idempotency) | 7 | Pass (root + wg0) |
| Migration unit (domain, preconditions, buildID) | 4 | Pass (any platform) |
| Migration integration (cross-node, end-to-end) | 2 | Gated by `MIGRATION_TEST=1` |
| **Total** | **48** | |

---

## File inventory

```
packages/orchestrator/internal/sandbox/network/v2/
├── egress_gateway.go          # Gateway-side nftables SNAT
├── egress_gateway_test.go
├── egress_profile.go          # EgressProfile + EgressManager
├── egress_profile_test.go
├── host_firewall.go           # O(1) host firewall (sets + chains)
├── host_firewall_test.go
├── ip_forward.go              # Migration IP forwarding + DNAT
├── ip_forward_test.go
├── migration.go               # MigrationCoordinator + preconditions
├── migration_test.go
├── network.go                 # Namespace/veth/tap create + teardown
├── network_test.go
├── ns_nat.go                  # In-namespace nftables SNAT/DNAT
├── ns_nat_test.go
├── observability.go           # eBPF veth observer (placeholder)
├── observability_test.go
├── policy.go                  # fwmark + policy routing
├── policy_test.go
├── pool.go                    # V2Pool (slot pooling)
├── publish_profile.go         # PublishProfile stub
├── slot.go                    # SlotV2 + registry
└── slot_test.go

packages/orchestrator/cmd/migrate-demo/
└── main.go                    # Standalone migration demo tool

docs/
├── migration-testing.md       # Testing & verification guide
└── network-v2-summary.md      # This document
```
