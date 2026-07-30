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
