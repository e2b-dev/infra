# Monad E2B runtime template

This directory builds the Monad runtime through the deployed E2B API and its
template-manager Nomad job. It pins a clean TAMS revision, compiles the
Linux/amd64 `monad-agent` and `monad` binaries from that source, and uploads
the `e2b.Dockerfile` recipe with those artifacts.

The build is fail-closed:

- `TAMS_CHECKOUT` must be clean and at the exact revision in
  `runtime-source.json`;
- the `apps/sandbox` tree and every runtime input tree are recorded;
- the Docker recipe and E2B start/readiness definition are content-addressed;
- asset preparation must run on a native Linux/amd64 Docker host and boot the
  exact prepared daemon in a privileged, network-isolated private cgroup;
- that local boot must publish the exact root-owned marker and root-only
  credential-bootstrap socket without receiving any credential before an E2B
  build is accepted;
- immutable source provenance is stored in
  `/opt/monad/runtime-provenance.json` because E2B build environments are not
  runtime environments;
- the template reference must be an immutable `monad-runtime:<tag>`;
- the E2B SDK is exactly `2.21.0`; and
- the desktop base is the exact amd64 digest of LinuxServer Webtop's Ubuntu i3
  Selkies image; KasmVNC, noVNC, and TigerVNC are rejected.

E2B's guest systemd remains the outer PID 1. The start command creates a child
PID namespace in which s6-overlay `/init` is PID 1; its native Webtop graph
supervises Xvfb, i3, Selkies, and nginx, while `svc-monad-agent` adds the Monad
entrypoint as an independent longrun. The network namespace remains shared
with the guest. Webtop HTTP/HTTPS are remapped to 6080/6081, the daemon listens
on 8000, and the unneeded nested-Docker packages are removed. Webtop's
`svc-docker` guard remains supervised but sleeps because `START_DOCKER=false`;
the E2B guest's outer infrastructure dockerd is not part of the desktop PID
and mount namespaces.

The Monad daemon remains a root control process outside the ordinary-session
tenant cgroup. Before it admits tenant work, it validates the root-owned
`/etc/monad/session-rebind-tenant-boundary.json` build claim against the exact
daemon and admission-helper bytes, creates and probes the cgroup-v2 boundary,
then publishes a root-protected readiness marker. Git, OpenCode, Chromium, and
workspace subprocesses enter that boundary through the attested admission
helper. The tenant-facing Webtop longruns—nginx, Xorg, D-Bus, PulseAudio,
Selkies, the desktop environment, watchdog, and xsettingsd—also block in the
same join-only helper until the marker exists, join the exact cgroup, shed
loader and shell-startup hooks, and then execute their preserved upstream root
setup. The upstream scripts retain their own `s6-setuidgid abc` transitions;
the image removes `abc` from the `sudo` and `docker` groups first, leaving only
primary group `1001` and supplementary group `100` for `abc` work. The pinned
nginx root master (`0:0`, group `0`) and its `www-data` workers (`33:33`, group
`33`) remain inside the tenant cgroup. `RESTART_APP=false` makes the root
watchdog an exact childless `sleep infinity` guard; any child is verification
failure. The Webtop cron
longrun is immutably replaced with `sleep infinity`, while its Docker guard is
retained but inert under `START_DOCKER=false`. A rebind fence removes the marker
before cancelling and draining tenant activity, so no supervised desktop
process can start outside the new generation's boundary.

At every s6 daemon start, the longrun creates `/run/monad-admission` if absent
and otherwise accepts only a real root-owned directory with mode `0700`. The
image also bakes both credential-bootstrap-required and
tenant-boundary-required policy; a missing or invalid attestation is therefore
a boot failure, not a legacy-mode fallback.

The JSON file is a prospective, content-bound build claim—not proof that a
runtime has enforced it. Registration becomes authoritative only after
`verify-runtime.mjs` independently observes the exact hashes, ownership and
modes, root-daemon separation, non-root desktop identities and groups, all
eight s6 leaders and their important descendants, exact nginx/watchdog roles,
marker basename/direct-parent placement, absence of cron/crond and nested dockerd, desktop behavior, and
zero-sandbox cleanup. Only that verified immutable template ID/ref/image triple
may be written to the protected TAMS environment.

The operator team used for this digest-pinned desktop build has 1248 MiB of
default build-disk free-space entitlement (512 MiB tier default plus a
736 MiB operator add-on). This is a template-manager entitlement, not a
customer sandbox resource setting. npm's boot cache is redirected to tmpfs so
it cannot consume durable session space.

Run:

```bash
export TAMS_CHECKOUT=/absolute/path/to/clean/tams
export E2B_API_KEY=...
export E2B_API_URL=https://api.e2b.monad0.net
export E2B_DOMAIN=e2b.monad0.net
export E2B_TEMPLATE_REF=monad-runtime:desktop-<content-version>

make build-monad-runtime-template
```

The build command intentionally fails before invoking E2B's `Template.build`
path when Docker is not a native `linux/amd64` engine. Emulation is not accepted
for the privileged cgroup preflight.

Verify the resulting reference in a network-denied synthetic sandbox,
including real Chromium execution, internal HTTP/HTTPS desktop checks,
external traffic-token access to port 6080, s6 service state, and
zero-sandbox cleanup:

```bash
make verify-monad-runtime-template
```

The result is the non-secret identity triple:

```text
E2B_TEMPLATE=<template_id>
E2B_TEMPLATE_REF=<immutable name:tag>
E2B_IMAGE_ID=<build_id>
```

Set all three in the TAMS `dev` GitHub environment, not at repository scope.
