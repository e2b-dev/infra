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

## Repo-specific review checks

<!-- Generated from the E2B architecture record, 2026-08-03. Regenerated periodically. -->

- Contract surfaces (`spec/openapi.yml`, any `.proto`, `packages/**/migrations/**`, and the semantics of values persisted and consumed elsewhere — network/transform rules, routing keys, token claims): ask where consumer-side behavior was confirmed. SDKs consume the spec via a pinned copy, dashboard/console carry synced mirrors, and closed-source runtime components consume orchestrator-side contracts. "No active consumer in this repo" is not verification — the consumer that matters may not live in this repository.
- Accepting a broader input shape (wildcards, prefixes, normalization) for a value another component looks up by exact match turns a loud create-time error into a silent runtime no-op. Ask for the consumer's matching semantics.
- A new migration must be versioned above main's newest; a `NOT NULL` column added to a table other systems copy is consumer-breaking until shown otherwise.
- Changes near envd versioning or its wire surface must address already-running sandboxes with older envd.
- Nomad `type = "system"` jobs have no `update` stanza — `progress_deadline` reasoning does not apply to them.
- Do not post claims about the outside world (a release exists, an upstream tool rejects a config) without stating how they were verified.
- Before requesting tests, check the file's recent PR history — do not re-request tests a maintainer deliberately removed.
- Terraform modules consumed via release-please tag refs are pinned by design; never flag tag pins as mutable-ref issues.
