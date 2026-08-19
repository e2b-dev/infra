# E2B Kubernetes/Kata provider for OKD

This provider runs E2B sandboxes as ordinary Kubernetes Pods backed by either
the `kata-clh` or `kata-qemu` RuntimeClass. It does not deploy Firecracker,
Nomad, host networking, privileged Pods, NBD, UFFD, or host mounts.

The controller implements E2B's existing `SandboxService`, `InfoService`, and
port-5007 proxy contracts, so the API and client-proxy keep their existing
interfaces. A caller selects a runtime with sandbox metadata:

```json
{"e2b.runtime-class":"kata-clh"}
```

`kata-clh` is the default; `kata-qemu` is the supported fallback. The operator
controls the allow-list through `KATA_RUNTIME_CLASSES`. In a mixed E2B fleet,
an explicit runtime selection is also enforced during API placement, so the
request cannot fall through to a Firecracker orchestrator.

## Phase-one contract

Implemented:

- OCI-backed create, envd `/init`, process/filesystem APIs, arbitrary port
  routing, timeout update, kill, CIDR egress policy, and restart-safe List;
- a non-privileged controller and per-sandbox Pod, Secret, and NetworkPolicy;
- default-deny access to private, loopback, link-local, and metadata CIDRs;
- a dedicated, tokenless `e2b-sandbox` ServiceAccount that receives only the
  OpenShift `anyuid` SCC, because envd must be UID 0 inside the isolated Kata
  guest to launch processes as arbitrary sandbox users;
- request idempotency and a distinct connection pool key for every Pod
  lifecycle;
- namespace-scoped Pod/Secret informer caches, so sandbox traffic does not
  issue Kubernetes API calls per request;
- both `kata-clh` and `kata-qemu` behind one runtime-neutral module.

Rejected before a Pod is created:

- snapshot resume, pause/checkpoint, auto-pause, and auto-resume;
- volumes, workload IAM, BYOP proxies, and domain egress rules.

These are explicit capability boundaries. A Kubernetes Pod restart is not a
memory snapshot, so pause/resume must not be reported as supported until a
durable PVC/checkpoint design has recovery and node-loss tests.

## Read-only plan

```bash
make PROVIDER=okd plan OKD_CONTEXT=indentia-ap
```

The preflight verifies both RuntimeClasses, candidate nodes, API discovery
support, Kubernetes resource APIs, current namespace permissions, and
MachineConfigPool health. Current Ready-Pod evidence for each runtime is
reported separately; run fresh canaries before production cutover. It creates
nothing.

Render the exact manifests that would be applied:

```bash
make -C iac/provider-okd render \
  CONTROLLER_IMAGE=registry.example/e2b/kubernetes-orchestrator:sha-... \
  SANDBOX_IMAGE_TEMPLATE='registry.example/e2b/{template_id}:{build_id}' \
  SANDBOX_CPU_ARCHITECTURE=x86_64 \
  SANDBOX_CAPACITY_CPU=96 \
  SANDBOX_CAPACITY_MEMORY_MIB=196608
```

Build the controller image from the repository root with:

```bash
docker build -f iac/provider-okd/Dockerfile \
  -t registry.example/e2b/kubernetes-orchestrator:sha-... .
```

`apply` is guarded and is not part of this implementation handoff. When a
maintainer has reviewed the rendered output, supplied immutable images, and
approved cluster mutation, it requires `OKD_APPLY=YES` explicitly.

## E2B control-plane wiring

Run the API with:

```text
SERVICE_DISCOVERY_PROVIDER=kubernetes
K8S_NAMESPACE=e2b
K8S_ORCHESTRATOR_POD_LABEL_SELECTOR=app.kubernetes.io/name=orchestrator
```

The included controller NetworkPolicy accepts control/proxy traffic only from
Pods labelled `app.kubernetes.io/name=api` or
`app.kubernetes.io/name=client-proxy` in the `e2b` namespace. Match those
labels in the control-plane manifests or provide an equivalent policy in an
overlay; sandbox Pods are deliberately excluded from the trusted gRPC path.

The controller is a one-replica StatefulSet. Its stable Pod name is its E2B
node identity; Kubernetes discovery uses its Pod IP because it does not use
host networking. Configure `SANDBOX_CAPACITY_CPU` and
`SANDBOX_CAPACITY_MEMORY_MIB` to the schedulable Kata pool, not total cluster
capacity, so E2B placement cannot intentionally overcommit that pool.
`SANDBOX_CPU_ARCHITECTURE` must describe that pool (`x86_64` for the current
`indentia-ap` nodes), not the node on which the controller happens to run.

The sandbox OCI image must contain `/usr/bin/envd` and expose envd on port
49983. It is started as:

```text
/usr/bin/envd -isnotfc -no-cgroups -verbose
```

For private registries, provision pull credentials separately and attach them
to `e2b-kubernetes-orchestrator` for the controller image and `e2b-sandbox`
for sandbox images before applying this provider. No registry credential is
stored in these manifests.

The sandbox Pod explicitly runs envd as UID 0, while privilege escalation,
host networking, host PID/IPC, host paths, and service-account tokens stay disabled.
UID 0 is inside the Kata VM boundary; the controller remains non-root and is
not granted the `anyuid` SCC.

Sandbox egress allows public IP space by default but always excludes E2B's
protected private, loopback, link-local, and metadata CIDRs. A caller can
narrow access with allowed or denied CIDRs; an allow-list that overlaps a
protected range is rejected before Pod creation. Domain policies and BYOP
proxies remain outside the phase-one contract. Pods use the public resolvers
from `SANDBOX_DNS_NAMESERVERS` (`8.8.8.8` by default), never the cluster DNS;
when using an explicit CIDR allow-list, include the chosen resolver if DNS is
required.

Template/build identifiers are substituted into `SANDBOX_IMAGE_TEMPLATE`.
That OCI publication contract is intentionally separate from the existing
Firecracker template-manager artifact format.
