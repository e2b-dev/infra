# Cost Estimation and Networking Tradeoffs

## Cost Estimation (Monthly - Estimated)
Based on a single-node / minimal cluster setup in `us-central1`:

| Component | GCP Resource | Approx. Cost (USD) |
|-----------|--------------|--------------------|
| Control Server | e2-standard-2 | $50.00 |
| API Server | e2-standard-4 | $100.00 |
| Orchestrator Node | n1-standard-8 | $150.00 |
| Load Balancer | Global External Application LB | $18.00 |
| Cloud NAT | Router + NAT Base Fee | $35.00 |
| Cloud DNS | Managed Zone + Queries | $2.00 |
| Storage | SSD Persistent Disks (200GB+) | $40.00 |
| **Total** | | **~$395.00 / month** |

*Note: Costs can be significantly reduced using Spot/Preemptible instances for the Orchestrator/Client nodes.*

## Networking Tradeoffs: Cloud NAT vs. Instance-based Static IP

### 1. Cloud NAT (Implemented)
- **Pros**:
  - **Scalability**: Seamlessly handles hundreds of nodes without assigning public IPs to each.
  - **Security**: Nodes are truly private; no unsolicited inbound traffic.
  - **Management**: Managed by GCP; no NAT instance to patch or monitor.
- **Cons**:
  - **Cost**: Base hourly charge plus data processing fees ($0.045/GB).
  - **Complexity**: Requires Cloud Router.

### 2. Instance-based Static IP (Public IP on VM)
- **Pros**:
  - **Simplicity**: No Router or NAT configuration.
  - **Cost**: No NAT processing fees (only standard egress).
- **Cons**:
  - **Security Risk**: Every instance has a public IP (even if firewalled), increasing the attack surface.
  - **Static IP Management**: Harder to ensure a *consistent* outbound IP for a cluster if nodes are replaced.

**Decision**: We chose **Cloud NAT** for this setup to ensure a "production-ready" posture where all sandbox/orchestrator nodes share a single, predictable, and highly available outbound static IP without exposing them to the public internet.
