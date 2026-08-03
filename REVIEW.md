# REVIEW.md — e2b-dev/infra
<!-- Generated from the E2B architecture record, 2026-08-03. Regenerated periodically —
     propose rule changes upstream rather than hand-editing here. -->

## Severity

- **Important** is reserved for: correctness bugs, contract/wire/schema compatibility,
  migrations, security, and regressions for existing callers. Everything else is a nit.
- At most **5 nits** per review; mention the rest as a count.
- After the first review of a PR, post **Important findings only** on re-reviews.

## Verification bar

- Any behavior claim needs a `file:line` citation from this repository.
- Any claim about the outside world (a release exists, an upstream API behaves a certain way,
  a tool rejects a config) must state how it was verified. If it cannot be verified from this
  repository, phrase it as a question, not a finding.

## Always check

1. **Contract surfaces** — `spec/openapi.yml`, any `*.proto`, `packages/**/migrations/**`,
   and the semantics of values that are persisted and consumed elsewhere (network/transform
   rules, routing keys, token claims). For any such change: name the downstream consumers
   (SDKs consume the spec via a pinned copy; dashboard and console carry synced spec mirrors;
   closed-source runtime components consume orchestrator-side contracts) and require the PR to
   state where consumer-side behavior was confirmed. Treat "this field has no active consumer
   in this repo" as **unverified until the PR names where consumers were checked** — the
   consumer that matters may not live in this repository.
2. **Matching-semantics changes** — accepting a broader input shape (wildcards, prefixes,
   normalization) for a value that another component looks up by exact match turns a loud
   create-time error into a silent runtime no-op. Flag the mismatch class explicitly and ask
   for the consumer's matching semantics.
3. **Migrations** — new migration versioned above main's newest; a `NOT NULL` column added to
   a table that other systems copy or mirror is a consumer-breaking change until shown
   otherwise.
4. **envd compatibility** — changes near envd versioning or its wire surface must address
   already-running sandboxes with older envd.
5. **Nomad jobs** — `type = "system"` jobs have no `update` stanza: `progress_deadline`
   reasoning does not apply to them; do not flag `kill_timeout` against it.

## Do not flag

- Anything the linters/typecheckers/CI already catch (golangci-lint, terraform validate,
  generated-code checks); code style and formatting.
- Generated files (`*.gen.go`, generated clients) and lockfiles.
- Terraform modules consumed by release-please **tag refs** — tag pins are by design, never a
  mutable-ref finding.
- Release-please PRs whose changes are only CHANGELOG/manifest.
- Test-coverage demands for behavior covered by the integration suites, and re-requests for
  tests that a recent PR deliberately removed — check the file's recent PR history before
  demanding tests.
- Pre-existing issues on lines the diff didn't modify (mention only if the diff makes them
  worse).

## Style

Terse, concrete, no summaries of the diff, no closing action-item lists, no emojis.
One thread per distinct issue.
