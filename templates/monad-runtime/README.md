# Monad E2B runtime template

This directory builds the non-desktop Monad runtime through the deployed E2B
API and its template-manager Nomad job. It pins a clean TAMS revision, compiles
the Linux/amd64 `monad-agent` and `monad` binaries from that source, and uploads
the single-stage `e2b.Dockerfile` recipe with those artifacts.

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
- PR A refuses any Kasm, Selkies, or VNC layer.

The operator team used for template builds must have at least 4096 MiB of
default build-disk free space. This is a template-manager entitlement, not a
customer sandbox resource setting.

Run:

```bash
export TAMS_CHECKOUT=/absolute/path/to/clean/tams
export E2B_API_KEY=...
export E2B_API_URL=https://api.e2b.monad0.net
export E2B_DOMAIN=e2b.monad0.net
export E2B_TEMPLATE_REF=monad-runtime:base-<content-version>

make build-monad-runtime-template
```

Verify the resulting reference in a network-denied synthetic sandbox, including
real Chromium execution and zero-sandbox cleanup:

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
