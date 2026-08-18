# AGENTS.md

Instructions for AI agents working in this repository.

## Understanding the repository

Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) before working on or reviewing code here. It describes what each service does, how services interact (sandbox creation, traffic routing, pause/resume, template builds), the data stores, and the deployment topology — it is the fastest way to build correct context about this codebase.

Keep that document updated: if a code change alters anything it describes (service responsibilities, ports, protocols, data stores, flows, deployment topology), update `docs/ARCHITECTURE.md` as part of the same change. When reviewing a PR that changes such details without updating the document, flag it.

## Review guidelines (pull requests)

Output style rules (strict, override any defaults):

- Only report concrete bugs, regressions, or correctness issues. Do not summarize the PR, the diff, or what changed.
- One short paragraph per finding. No preamble, no closing remark.
- No headers, no bullet lists, no tables, no diagrams.
- No emojis. No severity tags ("bug", "nit", "suggestion", "enhancement"). No labels.
- No branding or footer lines.
- If there are no real issues to flag, post no review at all.

## Scope

- Skip style/nit comments — `golangci-lint` covers those.
- Skip test-coverage comments — Codecov covers those.
- Focus on: race conditions, nil-deref, error handling, auth/authz, request routing, resource leaks, SQL/migration correctness, and gRPC/proto compatibility.

## Upstream sync (monthly)

This repository is a fork of [e2b-dev/infra](https://github.com/e2b-dev/infra). Upstream
moves fast (~150 commits/month); unharvested drift is how security fixes get missed and
how every later cherry-pick turns into a hand-port. Run a harvest **monthly** (first week
of the month), and immediately after any upstream security advisory.

Discipline (what the 2026-08 harvest did — keep this shape):

1. `git remote add upstream https://github.com/e2b-dev/infra.git && git fetch upstream`
   (the remote is not committed; add it per checkout).
2. Enumerate: `git log --right-only --cherry-pick --no-merges origin/main...upstream/main`.
   Classify each commit: security / placement-orchestrator / snapshot-pause /
   template-builder / feature / chore. Subject-match against our own commits too —
   this fork sometimes lands an upstream patch before a sync, and `--cherry-pick`
   only detects patch-identical picks.
3. Compute overlap: which upstream commits touch files this fork has modified
   (`git diff --name-only $(git merge-base origin/main upstream/main) origin/main`).
   Low-overlap security and correctness fixes are must-take; entangled ones become
   deliberate ports with the adaptation described in the commit message
   (`cherry picked from commit <sha>` + what was changed and why).
4. Apply on a `monadex/upstream-harvest-YYYYMMDD` branch, oldest first. Never
   hand-merge a commit that collides semantically with fork-side work inside the
   harvest PR — list it as a follow-up with its own reconciliation PR.
5. Verify: `go build ./...` and the unit tests (Monad Go Checks runs both on Linux in
   CI — a darwin machine cannot even type-check the linux-only orchestrator packages),
   plus `terraform fmt/validate` at the pinned version. Rollout to the fleet is a
   separate operator-approved step; the harvest PR never deploys anything.
6. Record in the PR body: the classification tally, what was taken, what was
   deliberately skipped and why, and which skips are decisions someone must
   eventually make (feature trains, removals of components we still run).

Standing decision list to revisit each sync (first written 2026-08-18):

- **docker-reverse-proxy**: upstream deleted it; we still build and deploy it.
  Keep-or-replace is an architecture decision, not a cherry-pick.
- **Local network slot storage**: upstream's hardening (leaked-namespace reclaim,
  non-sticky foreign-namespace detection) collides with our own PR #76 hardening;
  needs a dedicated reconciliation PR.
- **Template-builder distro train** (upstream #3411 and its fix stack): distro-aware
  provisioning, Alpine/NixOS bases, boot-time chrony. All template-builder fixes
  upstream now land on top of it; the longer we wait the more we hand-port.
- **Dependency and Go toolchain bumps**: take deliberately in their own PR
  (go.work/go.mod conflicts are mechanical but noisy in a harvest diff).
