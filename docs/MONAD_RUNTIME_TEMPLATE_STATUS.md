# Monad runtime template status

Last updated: 2026-07-30

## Remote-desktop entry gate

KasmVNC is **aspirational/stale terminology**, not a live or deprecated build
component:

- no current Dockerfile, template definition, workflow, or infrastructure
  artifact in TAMS or this repository installs KasmVNC;
- repository history shows that the removed `sandbox/Dockerfile` used
  `lscr.io/linuxserver/webtop:latest`, its `SELKIES_*` configuration, and
  s6-overlay;
- the current web desktop component still identifies the port-6080 surface as
  Selkies; and
- a live synthetic sandbox created from
  `monad-gcp-canary-base:infra-36c8d3cdd` had no Kasm, Selkies, noVNC, VNC,
  Xvfb, window-manager, or s6 packages/processes/paths and no listener on
  6080/6081. Audit sandbox `ih99gsf51n0msxg1m76cc` was destroyed and the
  synthetic team returned to zero sandboxes.

The implementation selected for the desktop PR is **LinuxServer Webtop's
Selkies stack on its Ubuntu i3 image**, with s6-overlay supervision and its
HTTP/HTTPS listeners remapped to the TAMS contract ports 6080/6081. KasmVNC
will not be introduced.

## PR A: base runtime

Status: live build and synthetic runtime verification passed; pending
publication.

The base template contains the Monad daemon and CLI compiled from the pinned
TAMS source, OpenCode, Git, the full Playwright driver and Chromium, and
agent-browser. It deliberately contains no desktop layer.

Pinned source:

```text
TAMS revision=41ee498773c1524cab763ca1616faab0b16f5de5
apps/sandbox tree=322a658913532aa66a9c946cb3797f19b660fcca
runtime content version=022dba47350a103b539b79f978af5fe536f93407ba7b6c31e983b6403f933739
```

The first live build attempt, reference
`monad-runtime:base-375e8b68a614`, registered template
`rdtms8je5mx3qrtxn1ap` and build
`a8decd98-9c49-423a-8cbc-05959298cb80`, then failed during package
installation with `ENOSPC`. The canary builder team still had the default
512 MiB free-space entitlement. The failed build was not promoted or used by a
sandbox.

The operator-only canary builder team now has a scoped 3584 MiB disk add-on
(`3ef3bd4b-676d-472f-9362-079877796496`), giving template builds 4096 MiB
default free space while leaving the 25600 MiB maximum unchanged. This is a
build entitlement only; it is not a customer tier or routing change. A
successful 6144 MiB-free candidate measured 7128 MiB total and was rejected;
the constrained accepted build measures 4952 MiB, below the 5120 MiB ceiling.

The build recipe also caught and corrected two integration assumptions before
acceptance:

- readiness uses the daemon's public `/monad/health` route and requires
  `daemon=ok`, `opencode=ok`, and `runtimeReady=true`; `/health` is an
  authenticated OpenCode proxy route and correctly returned 503 without a
  runtime token; and
- E2B `setEnvs` values are build-only, so immutable source provenance is stored
  in `/opt/monad/runtime-provenance.json` instead of relying on runtime
  environment inheritance.

Live verification sandbox `i90afrk2f2rlkhmgtl0r5` proved:

- the daemon and OpenCode reached the ready contract;
- OpenCode 1.14.28, agent-browser 0.27.0, Playwright 1.60.0, Git 2.47.3,
  the Monad CLI, and pinned source provenance were present;
- Playwright launched the installed Chromium and rendered a synthetic page;
- no desktop packages or listeners on 6080/6081 were present in PR A; and
- network egress was denied and cleanup returned the synthetic team to zero
  sandboxes.

Identity (populate only from a successful live build):

```text
E2B_TEMPLATE=rdtms8je5mx3qrtxn1ap
E2B_TEMPLATE_REF=monad-runtime:base-022dba47350a-5g
E2B_IMAGE_ID=1e598a66-24bd-42bc-9339-8355d26f90f1
```
