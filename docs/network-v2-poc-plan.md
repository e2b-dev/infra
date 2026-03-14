# E2B Network v2 PoC — Implementation Plan

## Context

The v0.3.1 network design document proposes moving E2B sandbox networking to a unified nftables model with O(1) rule lookup, eBPF observability, WireGuard host mesh, policy routing for egress profiles, and Firecracker snapshot/restore migration. This PoC validates the design on the infra-local test server (192.168.1.57) with w1 (192.168.100.253) as the v2 test node while box (192.168.100.137) stays on v1.

### Design Validation Summary

The v0.3.1 design is **sound and well-aligned** with the existing codebase:

- Per-sandbox netns, TAP, veth — **already implemented**, just naming convention differs
- nftables as primary firewall — **partially done**, TCP redirects still use iptables (11 iptables calls per sandbox create/destroy)
- No shared bridge — **already the case**
- Pre-pooled slots — **already done** (32 new + 100 reused)
- Host-local addressing — **already the model** (10.11.0.0/16 + 10.12.0.0/16)

**Actual gaps requiring new code for PoC:**
1. Migrate iptables → nftables (eliminate `coreos/go-iptables` from hot path)
2. O(1) nftables verdict maps (replace per-sandbox rule enumeration)
3. `NetworkVersion` config + `PoolInterface` abstraction for v1/v2 coexistence
4. tc/eBPF packet/byte counters on veth-host
5. WireGuard tunnel between box and w1 — also serves as **egress gateway data plane**
6. fwmark policy routing to route sandbox traffic through WireGuard to egress gateway
7. **Functional EgressProfile** — selects egress gateway + source IP per sandbox/customer
8. Stub data models: PublishProfile, MigrationDomain

### Test Server Capabilities (verified via SSH)

| Capability | Box VM | Worker w1 | Status |
|---|---|---|---|
| nftables v1.0.2, concat key maps | Yes | Yes | Ready |
| eBPF: kernel BPF/BTF/XDP | Yes | Yes | Ready (needs bpftool/clang install) |
| WireGuard kernel module | Yes | Yes | Ready (needs wireguard-tools install) |
| Go 1.25.8 | Yes | Yes | Can build from source |
| Policy routing (ip rule fwmark) | Yes | Yes | Tested, working |
| Infra repo at /opt/e2b/infra | Yes | Yes | Available |
| nftables concat key map | Tested OK | Same kernel | Verified |

**w1 is ideal for v2**: zero load, 3.2GB free RAM, 35GB free disk, identical kernel/toolchain to box.

---

## Architecture: What Changes

### Current (v1) per-sandbox hot path — 11 iptables calls

```
CreateNetwork():
  netlink: create netns, veth, tap, routes           (same in v2)
  iptables: 2 rules inside netns (SNAT + DNAT)       → replaced by nftables
  nftables: firewall init inside netns                (same in v2)
  iptables: 2 FORWARD rules on host                   → replaced by nftables set add
  iptables: 1 MASQUERADE on host                      → replaced by nftables set add
  iptables: 3 service redirects on host               → replaced by nftables map add
  iptables: 3 TCP proxy redirects on host             → replaced by nftables map add
```

### v2 per-sandbox hot path — 0 iptables calls, 1 nftables flush

```
CreateNetworkV2():
  netlink: create netns, veth, tap, routes            (reuse v1 code)
  nftables: NAT chains inside netns                   (replace 2 iptables rules)
  nftables: firewall init inside netns                (reuse v1 Firewall code)
  nftables: host_firewall.AddSlot()                   (single atomic flush):
    - add veth name to v2_veths set
    - add host CIDR to v2_host_cidrs set
    - add 3 entries to svc_redirect verdict map
    - add 3 entries to tcp_redirect verdict map
  tc: set fwmark on veth-host                         (new: policy routing)
  ebpf: attach counter on veth-host                   (new: observability, best-effort)
```

**Key improvement**: Host-level rules are O(1) hash lookups in nftables sets/maps. Rule count stays constant regardless of sandbox count. Current v1 has O(n) linear rule chains.

---

## File Plan

### New files (packages/orchestrator/internal/sandbox/network/v2/)

| # | File | Purpose |
|---|------|---------|
| 1 | `slot.go` | SlotV2 struct — extends base Slot with network_version, sandbox_id, execution_id, fwmark, profile IDs |
| 2 | `network.go` | CreateNetworkV2 / RemoveNetworkV2 — reuses netlink setup from v1, replaces iptables with nftables |
| 3 | `ns_nat.go` | In-namespace nftables NAT (SNAT/DNAT) replacing the 2 iptables rules inside netns |
| 4 | `host_firewall.go` | Singleton host-level nftables table `v2-host-firewall` with O(1) verdict maps |
| 5 | `observability.go` | tc/eBPF attachment for per-veth packet/byte counters |
| 6 | `observability_bpf.c` | Minimal BPF TC classifier (counter only, TC_ACT_OK passthrough) |
| 7 | `wireguard.go` | WireGuard interface setup + egress data plane tunnel |
| 8 | `policy.go` | fwmark allocation + ip rule/route table setup for egress routing |
| 9 | `pool.go` | V2Pool implementing PoolInterface — wraps Storage + v2 network creation |
| 10 | `egress_profile.go` | **Functional** EgressProfile — binds sandboxes to egress gateways, manages fwmark→route→SNAT |
| 11 | `egress_gateway.go` | Gateway-side nftables (forwarding + SNAT) — runs on box for PoC |
| 12 | `publish_profile.go` | PublishProfile stub data model |
| 13 | `migration.go` | MigrationDomain stub data model |
| 14 | `slot_test.go` | Unit tests: SlotV2 creation, fwmark uniqueness |
| 15 | `ns_nat_test.go` | Tests: nftables NAT in namespace (requires root+linux) |
| 16 | `host_firewall_test.go` | Tests: O(1) maps add/remove, element verification |
| 17 | `network_test.go` | Integration test: full v2 slot lifecycle (create → verify → teardown → verify clean) |
| 18 | `policy_test.go` | Tests: fwmark rule add/remove |
| 19 | `observability_test.go` | Tests: eBPF attach/detach/read counters (skip if no BPF) |
| 20 | `egress_profile_test.go` | Tests: EgressProfile routing setup, multi-sandbox shared profile |
| 21 | `egress_gateway_test.go` | Tests: gateway SNAT rules, forwarding from WireGuard |

### New files (network package root)

| # | File | Purpose |
|---|------|---------|
| 22 | `pool_iface.go` | `PoolInterface` — interface with Get/Return/Close methods |

### Modified files (minimal, backward-compatible)

| # | File | Change |
|---|------|--------|
| 23 | `packages/orchestrator/internal/sandbox/network/pool.go` | Add `NetworkVersion int` to Config struct |
| 24 | `packages/orchestrator/internal/sandbox/network/host.go` | Export `DefaultGateway()` function (currently private var) |
| 25 | `packages/orchestrator/internal/sandbox/sandbox.go` | Change `networkPool *network.Pool` → `network.PoolInterface` in Factory + NewFactory + getNetworkSlot |
| 26 | `packages/orchestrator/internal/server/main.go` | Change `NetworkPool *network.Pool` → `network.PoolInterface` in Server + ServiceConfig |
| 27 | `packages/orchestrator/main.go` | Version-gated pool construction (v1 or v2 based on NETWORK_VERSION env) |
| 28 | `packages/orchestrator/go.mod` | Add `github.com/cilium/ebpf`, `golang.zx2c4.com/wireguard/wgctrl` |

### Also affected (type signature ripple — compile-time verified)

| # | File | Change |
|---|------|--------|
| 29 | `packages/orchestrator/benchmark_test.go` | Pool type to PoolInterface (or use v1 Pool unchanged) |
| 30 | `packages/orchestrator/cmd/smoketest/smoke_test.go` | Same |
| 31 | `packages/orchestrator/cmd/create-build/main.go` | Same |
| 32 | `packages/orchestrator/cmd/resume-build/main.go` | Same |

---

## Detailed Implementation

### Step 1: PoolInterface extraction

**`packages/orchestrator/internal/sandbox/network/pool_iface.go`** (new):

```go
package network

type PoolInterface interface {
    Get(ctx context.Context, network *orchestrator.SandboxNetworkConfig) (*Slot, error)
    Return(ctx context.Context, slot *Slot) error
    Close(ctx context.Context) error
}
```

The existing `*Pool` already satisfies this interface (exact method signatures match). `Populate()` is called separately in main.go and not part of the interface.

**Changes to consumers** — `sandbox.Factory.networkPool`, `server.Server.networkPool`, `server.ServiceConfig.NetworkPool`, `sandbox.NewFactory()`, `getNetworkSlot()` all change from `*network.Pool` to `network.PoolInterface`. This is a pure type-level refactor; runtime behavior is identical.

The `cmd/` files (create-build, resume-build, smoketest, benchmark) pass `*network.Pool` which satisfies the interface — no functional changes needed, just parameter type annotations.

### Step 2: Config extension

Add to `Config` in `pool.go`:
```go
NetworkVersion int `env:"NETWORK_VERSION" envDefault:"1"`
```

Export default gateway in `host.go`:
```go
func DefaultGateway() string { return defaultGateway }
```

### Step 3: SlotV2

**`v2/slot.go`** — extends base Slot with v2 metadata:

```go
type SlotV2 struct {
    Slot            *network.Slot
    NetworkVersion  int
    SandboxID       string
    ExecutionID     string
    PolicyID        string
    EgressProfileID string
    PublishProfileID string
    FwMark          uint32    // 0x200 + slot.Idx
    WgPeerIndex     int
}
```

SlotV2 **wraps** `*network.Slot` (composition, not embedding) because `*Slot` flows through the existing sandbox pipeline unchanged. The V2Pool maintains a `sync.Map[int]*SlotV2` mapping slot index → v2 metadata, consulted only by v2-specific code paths (host firewall, observability).

### Step 4: In-namespace nftables NAT

**`v2/ns_nat.go`** — replaces the 2 iptables rules inside netns:

```go
func SetupNamespaceNAT(conn *nftables.Conn, table *nftables.Table,
    vpeerIface, hostIP, namespaceIP string) error
```

Adds two chains to the existing `slot-firewall` table (reuses the table the v1 Firewall already creates):
- `preroute_nat`: DNAT `hostIP` → `namespaceIP`
- `postroute_nat`: SNAT `namespaceIP` → `hostIP`

**SO_ORIGINAL_DST compatibility**: nftables `dnat to` / `snat to` use the same kernel conntrack as iptables REDIRECT/SNAT. The TCP firewall proxy's `SO_ORIGINAL_DST` call (`packages/orchestrator/internal/tcpfirewall/utils.go`) works unchanged.

### Step 5: Host firewall with O(1) verdict maps

**`v2/host_firewall.go`** — singleton, created once per orchestrator process:

```go
type HostFirewall struct {
    conn      *nftables.Conn
    table     *nftables.Table      // "v2-host-firewall"
    vethSet   *nftables.Set        // type ifname; for FORWARD matching
    cidrSet   *nftables.Set        // type ipv4_addr; flags interval; for MASQUERADE
    svcMap    *nftables.Set        // verdict map: ifname . ipv4_addr . inet_service → redirect
    tcpMap    *nftables.Set        // verdict map: ifname . inet_service → redirect
    defaultGw string
    mu        sync.Mutex
}
```

Chains:
- `forward`: `iifname @v2_veths oifname <gw> accept` + reverse
- `prerouting` (nat): `vmap @svc_redirect` then `vmap @tcp_redirect`
- `postrouting` (nat): `ip saddr @v2_host_cidrs oifname <gw> masquerade`

**AddSlot(slotV2)**: adds elements to all 4 sets/maps in one `conn.Flush()`.
**RemoveSlot(slotV2)**: removes elements from all 4 sets/maps in one `conn.Flush()`.

Rule count stays **constant** (3 chains x 1-2 rules each). Only set/map elements grow — hash-based O(1) lookup.

**Note on google/nftables v0.3.0**: This version supports `SetConcatTypeBits` for concat key types and `IsMap: true` for verdict maps. Verified concat maps work on the box VM's nftables v1.0.2 kernel.

### Step 6: V2 network creation

**`v2/network.go`**:

```go
func CreateNetworkV2(ctx context.Context, slot *network.Slot, slotV2 *SlotV2,
    hf *HostFirewall, observer *VethObserver) error
```

Reuses the netlink code pattern from v1's `network.go` (namespace, veth, tap, routes, loopback) but:
- **Replaces** iptables NAT inside netns → calls `SetupNamespaceNAT()`
- **Replaces** all host iptables calls → calls `hf.AddSlot(slotV2)`
- **Adds** fwmark via `SetupPolicyMark(slot.VethName(), slotV2.FwMark)`
- **Adds** eBPF via `observer.Attach(slot.VethName())` (best-effort, nil-safe)

`RemoveNetworkV2` does the reverse.

### Step 7: V2Pool

**`v2/pool.go`** — implements `network.PoolInterface`:

```go
type V2Pool struct {
    config      network.Config
    storage     network.Storage
    hostFw      *HostFirewall
    observer    *VethObserver
    newSlots    chan *network.Slot
    reusedSlots chan *network.Slot
    done        chan struct{}
    doneOnce    sync.Once
    v2meta      sync.Map  // slot.Idx → *SlotV2
}
```

Same dual-pool architecture as v1 (pre-created + reused channels). The key difference: `createNetworkSlot()` calls `CreateNetworkV2()` instead of `slot.CreateNetwork()`.

### Step 8: tc/eBPF observability

**`v2/observability_bpf.c`**: Minimal TC classifier — BPF_MAP_TYPE_HASH keyed by ifindex, value = {packets, bytes}. Returns TC_ACT_OK (passthrough). **Observability only, never drops.**

**`v2/observability.go`**: Uses `cilium/ebpf` with `bpf2go` for compile-time BPF embedding:
```go
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 counter observability_bpf.c
```

This pre-compiles the BPF object during `go generate` — no clang needed on the target machine at runtime.

```go
type VethObserver struct { ... }
func NewVethObserver() (*VethObserver, error)     // nil-safe: returns nil,nil if BPF unavailable
func (o *VethObserver) Attach(vethName string) error
func (o *VethObserver) Detach(vethName string) error
func (o *VethObserver) ReadCounters(vethName string) (packets, bytes uint64, err error)
```

### Step 9: WireGuard mesh + egress gateway data plane

**`v2/wireguard.go`**: Sets up WireGuard interface using `vishvananda/netlink` (already a dep) for link creation and `wgctrl` for peer configuration.

```go
type WireGuardConfig struct {
    PrivateKey    string
    ListenPort    int
    InterfaceName string   // "wg0"
    Address       string   // "10.99.0.X/24"
    Peers         []WireGuardPeer
}
func SetupWireGuard(ctx context.Context, cfg WireGuardConfig) error
func TeardownWireGuard(ifname string) error
```

PoC addressing: w1 (compute node) gets `10.99.0.2/24`, box (egress gateway) gets `10.99.0.1/24`. Keys pre-generated, passed via env vars.

**The WireGuard tunnel is not just a mesh — it's the egress data plane.** Sandbox traffic marked with a fwmark is policy-routed through the WireGuard tunnel to box, which acts as the egress gateway and SNATs with a specific public IP.

This models production exactly:
```
Production:  compute host → fwmark → policy route → VPC route → egress gateway → SNAT → internet
PoC:         w1 (compute) → fwmark → policy route → wg0 tunnel → box (gateway) → SNAT → internet
```

### Step 10: EgressProfile + policy routing (functional, not stub)

The EgressProfile is the **key new abstraction** — it binds a sandbox to a specific egress identity.

**`v2/egress_profile.go`**:

```go
type EgressProfile struct {
    ID            string
    OwnerType     string   // "customer" or "sandbox"
    OwnerID       string
    Region        string
    Mode          string   // "customer-shared" | "sandbox-dedicated"
    BackendType   string   // "gateway" | "cloud_nat" | "direct"
    PublicIPSet   []net.IP // the sticky outbound IPs
    GatewayAddr   net.IP   // egress gateway WireGuard IP (e.g., 10.99.0.1)
    GatewaySNATIP net.IP   // the IP the gateway SNATs to
    FwMark        uint32   // routing mark for this profile
    RouteTableID  int      // policy routing table ID
}

type EgressManager struct {
    profiles map[string]*EgressProfile  // profile ID → profile
    mu       sync.RWMutex
}

func NewEgressManager() *EgressManager
func (m *EgressManager) Register(profile *EgressProfile) error
func (m *EgressManager) GetProfile(id string) *EgressProfile
func (m *EgressManager) SetupRouting(profile *EgressProfile) error   // ip rule + route table → wg0
func (m *EgressManager) TeardownRouting(profile *EgressProfile) error
```

**How it works end-to-end on the PoC:**

1. **On w1 (compute node):**
   - EgressProfile "customer-A" has `FwMark=0x300`, `GatewayAddr=10.99.0.1`, `RouteTableID=300`
   - `ip rule add fwmark 0x300 lookup 300`
   - `ip route add default via 10.99.0.1 dev wg0 table 300`
   - Sandbox slot gets assigned profile → nftables marks outbound traffic with `0x300`
   - Traffic exits through wg0 → arrives at box

2. **On box (egress gateway):**
   - Receives traffic on wg0 from w1
   - nftables SNAT: `ip saddr 10.11.0.0/16 oifname "enp0s2" snat to 192.168.100.200`
   - Traffic exits with the sticky IP `192.168.100.200`

3. **Verification:**
   - From inside sandbox: `curl http://ifconfig.me` → should show `192.168.100.200`
   - Different EgressProfile → different source IP

**`v2/policy.go`**: Per-profile fwmark + ip rule + routing table setup.

```go
func SetupPolicyMark(vethName string, fwmark uint32) error          // nftables mark in host prerouting
func SetupPolicyRoute(fwmark uint32, tableID int, gw net.IP, dev string) error  // ip rule + ip route
func TeardownPolicyRoute(fwmark uint32, tableID int) error
```

Multiple sandboxes can share the same EgressProfile (customer-shared mode). The fwmark is per-profile, not per-slot. This means one `ip rule` + one routing table entry per profile, not per sandbox.

**`v2/egress_gateway.go`** (new — setup script/helper for box side):

```go
// EgressGatewayConfig configures the gateway side (runs on box for PoC)
type EgressGatewayConfig struct {
    WgInterface   string   // "wg0"
    ExternalIface string   // "enp0s2"
    SNATRules     []SNATRule
}

type SNATRule struct {
    SourceCIDR string   // "10.11.0.0/16" (sandbox traffic)
    SNATIP     net.IP   // sticky IP to use
    FwMark     uint32   // match mark from compute node (optional, for per-profile SNAT)
}

func SetupEgressGateway(cfg EgressGatewayConfig) error   // nftables forwarding + SNAT
func TeardownEgressGateway(cfg EgressGatewayConfig) error
```

### Step 11: Stub data models (PublishProfile, MigrationDomain)

**`v2/publish_profile.go`**, **`v2/migration.go`**: Pure data structs with constructors. No behavior beyond `Default*()` factory functions. These exist to prove the schema works and to be wired into SlotV2. (EgressProfile is now functional, not a stub.)

### Step 12: Version-gated main.go

In `packages/orchestrator/main.go`, after storage creation:

```go
var pool network.PoolInterface
if config.NetworkConfig.NetworkVersion == 2 {
    hostFw, err := v2.NewHostFirewall(network.DefaultGateway(), config.NetworkConfig)
    // ...
    observer, _ := v2.NewVethObserver()  // best-effort
    v2p := v2.NewV2Pool(slotStorage, config.NetworkConfig, hostFw, observer)
    go v2p.Populate(ctx)
    pool = v2p
} else {
    v1p := network.NewPool(network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, slotStorage, config.NetworkConfig)
    go v1p.Populate(ctx)
    pool = v1p
}
```

---

## Execution Order

Each step is independently compilable and testable. Steps 1-2 are the only ones touching v1 code.

| Phase | Steps | What | Risk |
|-------|-------|------|------|
| **A: Interface** | 1-2 | PoolInterface + Config — pure refactor, zero behavior change | Low |
| **B: v2 core** | 3-6 | SlotV2, ns_nat, host_firewall, network.go — new code, standalone | Medium |
| **C: Pool** | 7 | V2Pool implementing PoolInterface | Low |
| **D: Wiring** | 12 | Version-gated main.go | Low |
| **E: Observability** | 8 | eBPF counters — best-effort, nil-safe | Low (isolated) |
| **F: Egress data plane** | 9-10 | WireGuard tunnel + EgressProfile + policy routing — full sticky IP path | Medium |
| **G: Egress gateway** | 11 (egress_gateway.go) | Box-side SNAT setup — completes the egress PoC | Low |
| **H: Stubs** | 12-13 | PublishProfile + MigrationDomain data models | None |
| **I: Tests** | All | Unit + integration tests at each step | N/A |

---

## Tests

### Unit tests (run on dev machine or w1, require root+linux)

| Test file | Tests | Requires |
|-----------|-------|----------|
| `v2/slot_test.go` | TestSlotV2_Creation, TestSlotV2_FwMarkUniqueness, TestSlotV2_EmbeddedSlotMethods | Go only |
| `v2/ns_nat_test.go` | TestNamespaceNAT_Setup, TestNamespaceNAT_ConntrackCompat | root, netns, nftables |
| `v2/host_firewall_test.go` | TestHostFirewall_Init, TestHostFirewall_AddRemoveSlot, TestHostFirewall_O1Lookup, TestHostFirewall_ConcurrentAccess | root, nftables |
| `v2/network_test.go` | TestCreateNetworkV2_FullLifecycle, TestCreateNetworkV2_NoIptablesRules, TestCreateNetworkV2_CleanTeardown | root, netns, nftables |
| `v2/policy_test.go` | TestPolicyRoute_Setup, TestPolicyRoute_Teardown | root |
| `v2/observability_test.go` | TestVethObserver_AttachDetach, TestVethObserver_CounterIncrement | root, BPF (skip if unavailable) |
| `v2/egress_profile_test.go` | TestEgressProfile_RouteSetup, TestEgressProfile_SharedByMultipleSlots, TestEgressProfile_Teardown | root |
| `v2/egress_gateway_test.go` | TestEgressGateway_SNATRules, TestEgressGateway_ForwardFromWg | root, nftables |

### Integration test (run on w1 with running v2 orchestrator)

**`v2/integration_test.go`** or standalone test script:

1. Start orchestrator with `NETWORK_VERSION=2`
2. Create a sandbox (via gRPC or API)
3. Verify: namespace exists, nftables rules present, NO iptables rules for the sandbox
4. Verify: eBPF counters exist on veth
5. From inside sandbox: `curl http://example.com` — verify egress works
6. Read eBPF counters — verify packets > 0
7. **Verify sticky egress IP**: from inside sandbox, `curl http://ifconfig.me` → should return the EgressProfile's SNAT IP (e.g., 192.168.100.200), NOT w1's own IP
8. Destroy sandbox
9. Verify: clean teardown, no leaked namespaces/interfaces/rules

### Egress gateway integration test (run on box)

1. Setup WireGuard tunnel (wg0) + egress gateway nftables rules
2. Add secondary IP on box's interface (`192.168.100.200/32`)
3. Verify: traffic arriving from w1 via wg0 gets SNATed to 192.168.100.200
4. Verify: different EgressProfile → different SNAT IP

### Manual validation commands (on w1)

```bash
# Verify nftables O(1) maps
sudo nft list map inet v2-host-firewall svc_redirect
sudo nft list map inet v2-host-firewall tcp_redirect
sudo nft list set inet v2-host-firewall v2_veths

# Verify NO iptables for v2 sandboxes
sudo iptables -t nat -L PREROUTING -n | grep veth  # should be empty

# Verify rule count is constant
sudo nft list chain inet v2-host-firewall prerouting | wc -l  # constant regardless of sandbox count

# Verify eBPF
sudo tc filter show dev veth-2 ingress

# Verify WireGuard tunnel
sudo wg show wg0
ping -c 3 10.99.0.1  # from w1 to box through tunnel

# Verify egress profile routing
ip rule list | grep 0x300                    # fwmark rule for EgressProfile
ip route show table 300                       # routes through wg0 to gateway

# Verify fwmark marking
sudo nft list chain inet v2-host-firewall mark_chain

# Verify sticky egress IP (from inside a sandbox namespace)
sudo nsenter --net=/var/run/netns/ns-2 curl -s http://ifconfig.me
# Should return the gateway's SNAT IP, NOT w1's own IP

# Verify egress gateway on box
sudo nft list table inet egress-gateway      # SNAT rules on box
```

---

## Deployment to w1

### Prerequisites (one-time on w1)

```bash
# SSH to w1 via jump host
ssh -J e2b@192.168.1.57 -i ~/.ssh/e2b_vm_key e2b@192.168.100.253

# Install WireGuard tools
sudo apt-get install -y wireguard-tools

# For eBPF development (only if recompiling BPF on w1)
sudo apt-get install -y linux-tools-$(uname -r) clang libbpf-dev
```

### Build and deploy

Option A — push branch to git, pull on w1:
```bash
# Dev machine:
git push origin feature/network-v2-poc

# On w1:
cd /opt/e2b/infra
sudo git fetch && sudo git checkout feature/network-v2-poc
cd packages/orchestrator
sudo CGO_ENABLED=1 go build -o bin/orchestrator .
```

Option B — rsync new files, build on w1:
```bash
# From dev machine:
rsync -avz -e "ssh -J e2b@192.168.1.57 -i ~/.ssh/e2b_vm_key" \
  packages/orchestrator/ e2b@192.168.100.253:/opt/e2b/infra/packages/orchestrator/
```

### Setup egress gateway on box (one-time)

```bash
# SSH to box
ssh -J e2b@192.168.1.57 -i ~/.ssh/e2b_vm_key e2b@192.168.100.137

# Install WireGuard tools
sudo apt-get install -y wireguard-tools

# Setup WireGuard (box side — gateway)
# Keys pre-generated, wg0 at 10.99.0.1/24, peer = w1 at 192.168.100.253:51820

# Add secondary IP for SNAT (simulates sticky public IP)
sudo ip addr add 192.168.100.200/32 dev enp0s2

# Setup egress gateway nftables (forwarding + SNAT from wg0 traffic)
# This is done by the egress_gateway.go helper or manually:
sudo nft add table inet egress-gateway
sudo nft add chain inet egress-gateway forward '{ type filter hook forward priority 0; policy accept; }'
sudo nft add rule inet egress-gateway forward iifname "wg0" oifname "enp0s2" accept
sudo nft add rule inet egress-gateway forward iifname "enp0s2" oifname "wg0" ct state established,related accept
sudo nft add chain inet egress-gateway postrouting '{ type nat hook postrouting priority 100; policy accept; }'
sudo nft add rule inet egress-gateway postrouting oifname "enp0s2" ip saddr 10.11.0.0/16 snat to 192.168.100.200
```

Box's v1 orchestrator continues running unchanged — the egress gateway is just nftables rules + WireGuard on the host.

### Run v2 orchestrator on w1

```bash
# Stop existing orchestrator
sudo pkill orchestrator

# Run with v2 networking
export NETWORK_VERSION=2
# ... (other env vars from existing orchestrator process)
sudo -E /opt/e2b/infra/packages/orchestrator/bin/orchestrator
```

---

## Key Reusable Code from v1

| What | File:Line | Reuse in v2 |
|------|-----------|-------------|
| Namespace + veth + tap creation | `network/network.go:19-166` | Extract netlink setup into shared helper, call from both v1 and v2 |
| Firewall (nftables sets + filter chain) | `network/firewall.go:37-103` | Reuse unchanged inside v2 namespace — same `NewFirewall()` call |
| IP allocation (GetIndexedIP) | `network/slot.go:90-153` | Reuse via `network.NewSlot()` — SlotV2 wraps it |
| Default gateway detection | `network/host.go:32-54` | Export and call from v2 |
| Storage interface | `network/storage.go:7-10` | Reuse unchanged — V2Pool uses same Storage backends |
| Pool metrics (OTEL counters) | `network/pool.go:27-48` | Reuse same metric names from V2Pool |
| TCP firewall proxy | `tcpfirewall/proxy.go` | Unchanged — nftables redirect feeds same proxy ports |
| ConfigureInternet / ResetInternet | `network/slot.go:256-328` | Reuse unchanged — operates on same Firewall inside same namespace |

---

## Remote Nodes Outside VPC

This design **explicitly supports** compute nodes outside the VPC in production:

- **WireGuard is transport-agnostic** — works over any IP path (public internet, cross-cloud, on-prem)
- **Egress gateway decouples compute from public identity** — sandbox traffic is SNATed at the gateway, not at the compute node
- **fwmark + policy routing is local** — each compute node manages its own routing tables independently
- **No VPC peering or special routing required** — WireGuard tunnel is the only connectivity needed between compute and gateway

Production topology for remote nodes:
```
Remote compute (any cloud/on-prem) → WireGuard tunnel → Egress gateway (in VPC) → SNAT → internet
```

The PoC validates this exact pattern: w1 routes sandbox traffic through wg0 to box.

---

## Risk Matrix

| Risk | Impact | Mitigation |
|------|--------|------------|
| nftables redirect vs SO_ORIGINAL_DST | High — TCP firewall breaks | Test in ns_nat_test.go before integration |
| google/nftables v0.3.0 concat map support | Medium — host firewall breaks | Standalone test in host_firewall_test.go first |
| Egress via WireGuard adds latency/MTU overhead | Low — acceptable for PoC | Production uses VPC routing not WireGuard; PoC validates mechanism |
| WireGuard tunnel doesn't carry fwmark across | Medium — SNAT can't differentiate profiles | Verify mark preservation or use IP-based matching at gateway |
| eBPF not available on w1 | Low — observability only | Nil-safe observer; skip test if no BPF |
| Breaking v1 path during refactor | High — box stops working | PoolInterface satisfies *Pool; compile-time check; box never gets NETWORK_VERSION=2 |

---

## Success Criteria

1. **v2 sandbox creates and destroys** on w1 with ZERO iptables rules
2. **nftables rule count stays constant** as sandbox count increases (verified with 1, 5, 10 sandboxes)
3. **Egress works** — sandbox can reach the internet through the nftables redirect → TCP firewall proxy path
4. **Firewall works** — private IP ranges blocked, allowed CIDRs work
5. **Sticky egress IP works** — `curl ifconfig.me` from sandbox returns the EgressProfile's SNAT IP, not w1's own IP. Different EgressProfiles → different source IPs
6. **WireGuard egress data plane** — sandbox traffic traverses wg0 to box (gateway), box SNATs with assigned IP, traffic exits to internet
7. **eBPF counters increment** when sandbox sends traffic
8. **Policy routing per-profile** — fwmark marks route through correct routing table → wg0 → gateway
9. **Box v1 unchanged** — still works exactly as before
10. **All unit tests pass** on w1
11. **Clean teardown** — no leaked namespaces, interfaces, nftables rules, eBPF programs, or ip rules after orchestrator shutdown
