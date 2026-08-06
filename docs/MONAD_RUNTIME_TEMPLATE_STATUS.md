# Monad runtime template status

Last updated: 2026-08-07

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

Status: merged in PR #39 at
`5f7c54bd286aeb7538eb8857bd80d5c9251e13f0`.

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

## PR B: Selkies desktop and s6 supervision

Status: live build and exact-artifact synthetic verification passed; PR B
contains the publication record.

The selected desktop base is the digest-pinned amd64 LinuxServer Webtop Ubuntu
i3 image:

```text
lscr.io/linuxserver/webtop@sha256:6da6d68a2e3309fdbdc945a1b5465c3174acdf19dfbd44734495b122f10a5236
upstream Webtop revision=4e3769d3731023d6a25d619d6bcfe00b66399a99
```

Webtop's existing s6-overlay graph supervises Xvfb, i3, Selkies, and nginx.
The Monad daemon is an additional `svc-monad-agent` longrun after
`init-services`. E2B keeps systemd as the outer guest PID 1, so the runtime
start command creates a child PID namespace where `/init` is PID 1 while
sharing the guest network namespace. HTTP and HTTPS are remapped from
3000/3001 to the TAMS contract ports 6080/6081. The unneeded Docker packages
from Webtop are removed. Its `svc-docker` guard remains in the s6 graph but
sleeps with `START_DOCKER=false`; an outer E2B guest dockerd, when present, is
outside the desktop PID and mount namespaces.

The live build sequence rejected three artifacts instead of promoting rounded
or incomplete evidence:

- build `09684587-4006-4609-bf1e-294461dbbfc9` proved that E2B retains
  systemd as guest PID 1, so starting `/init` directly violates s6's PID-1
  requirement;
- build `8fa9cf41-68b5-4279-9647-3e6123c4272b` proved the child PID namespace
  fix but measured 7783 MiB, above the 5120 MiB ceiling; and
- build `053403cd-f24d-4a16-bed8-3e6cd7b2bfad` measured 5133 MiB and booted
  successfully, but its started rootfs had no non-reserved durable free
  blocks. It also exceeded the provisional 5120 MiB accounting target by
  13 MiB and was not accepted.

Runtime content version
`54b32edc942dd0fab5923407f429debf545b854746ca30b1a36f190152b6bb68`
removes 353 MB of nested-Docker packages, moves npm's boot cache to tmpfs, and
content-addresses the s6 service files. Synthetic sandbox
`ik595ui1koos9rlkr5ccj` then proved:

- `/monad/health` reached `daemon=ok`, `opencode=ok`, and
  `runtimeReady=true`;
- OpenCode 1.14.28, agent-browser 0.27.0, Playwright 1.60.0, Git 2.53.0,
  the Monad CLI, and exact TAMS provenance were present;
- Playwright launched `/usr/bin/chromium`;
- s6 reported the Monad, Xorg, Selkies, nginx, and Docker-guard services up,
  while no nested dockerd or Kasm/noVNC/TigerVNC path was present;
- internal HTTP 6080 and HTTPS 6081 returned 200, and the authenticated
  external port-6080 surface returned the Selkies application HTML;
- the 4667 MiB rootfs retained 538 MiB available after a real 256 MiB
  write-and-fsync probe in `/workspace`; and
- network egress was denied and cleanup returned the operator team to zero
  sandboxes.

The operator team now has a scoped 736 MiB add-on, for 1248 MiB total default
build free-space entitlement and the unchanged 25600 MiB maximum. The accepted
build record reports 5133 MiB total and 1248 MiB requested free space. Repeating
the build after reducing the entitlement proved that 5133 MiB is this image
path's fixed artifact accounting floor; the started guest filesystem is
4667 MiB. The provisional 5120 MiB accounting target was therefore retired
after the operator removed the budget constraint; runtime acceptance is based
on the measured guest headroom and write probe.

Identity:

```text
E2B_TEMPLATE=rdtms8je5mx3qrtxn1ap
E2B_TEMPLATE_REF=monad-runtime:desktop-54b32edc942d-c5g
E2B_IMAGE_ID=886b9312-c1b4-4e09-907e-9fdc6a6dc872
```

## Current invited-beta runtime: brokered session credentials

Status: built, current-run cleanup verified, registered in the TAMS `dev`
environment, refreshed into the live API without changing the application
release, and exercised through an ordinary-session operator canary.

The immutable runtime input is TAMS revision
`7f1c409aae355be5e0cd8b1bc365f9cc806c96fd`, with content version:

```text
runtime content version=f5a9baea8a88118b58bcac64518f8c32d792df865e202fb6c1fd42a31c5b6016
```

The ownership-scoped verifier fix merged in infra revision
`a2dc0f0335c8eb66c05baa4d3841f10ce01fd6b7`. Its corresponding TAMS evidence
contract merged in PR #286 at
`2b254c697504c6547f52aad8ba9ac9b7756edafb`.

Resume verification run
[`31111780903`](https://github.com/rehoboam-lab/tams/actions/runs/31111780903)
recovered build run
[`31108551482`](https://github.com/rehoboam-lab/tams/actions/runs/31108551482)
without rebuilding the immutable image. The verifier allowed two unrelated
baseline sandboxes to remain, destroyed only the sandbox it created, and proved
that the current verification run had zero matching sandboxes after cleanup.

Registered identity:

```text
E2B_TEMPLATE=rdtms8je5mx3qrtxn1ap
E2B_TEMPLATE_REF=monad-runtime:desktop-f5a9baea8a88
E2B_IMAGE_ID=54184138-95c7-4675-9545-ad8813d087db
```

Live environment refresh run
[`31112104912`](https://github.com/rehoboam-lab/tams/actions/runs/31112104912)
installed that exact triple while preserving application revision
`7f1c409aae355be5e0cd8b1bc365f9cc806c96fd` and the existing digest-pinned
release images:

```text
API=sha256:5c207f178a5ebfcd16162df5d45fc2fc02c57ef49c64134ab7303838ffcd32b6
frontend=sha256:ce465a0f7806f679840897e011530860317c668699817d892ac8d372d4ce296c
sandbox=sha256:1173639553a9a59375e0d7ce18a4b61e51e5fa71c5ff3e4ecd9ab8631c677b89
```

Ordinary-session canary run
[`31112299980`](https://github.com/rehoboam-lab/tams/actions/runs/31112299980)
then exercised project `b4524b27-0be5-4aa9-ab19-3881e1ce9d27` through the
live session API. Readiness was 25.695 seconds and total lifecycle time was
43.994 seconds. The canary proved verified placement attestation,
authenticated Selkies access, one exchange followed by revocation for each of
the `git`, `model`, and `internal_service` credential leases, and zero matching
sandboxes in the independent post-cleanup inventory.

This is an operator-project admission result, not a global rollout claim. The
live rollout remains:

```text
ALLOWED_SANDBOX_PROVIDERS=local_docker,e2b
invited-beta rollout percentage=0
E2B warm pool enabled=false
admission=project-policy operator override only
```
