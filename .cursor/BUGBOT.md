# BugBot configuration

This file contains the configuration for the BugBot.

## PR description

- Don't list all the changes by files/change types, just very briefly summarize what is the PR trying to accomplish.
- Don't list the changes files/dirs.
- Don't use sections, or lists, ideally just one short paragraph.
- Don't use emojis.

## PR review

Please review this pull request and provide feedback on:

- Potential bugs or issues
- Performance considerations
- Important security concerns

Be constructive and helpful in your feedback and be **very conscise**.
Skip general recommendations, skip summarizing the changes, and don't make any recommendations on code style.
Also skip any summarization about why the PR was done well, focus only on the code changes and the potential issues.
Don't output final summaries, or final lists of action items.

## Repo-specific review rules

<!-- Generated from the E2B architecture record, 2026-08-03. Regenerated periodically. -->

- Contract surfaces (`spec/openapi.yml`, any `.proto`, `packages/**/migrations/**`, and the
  semantics of values persisted and consumed elsewhere — network/transform rules, routing
  keys, token claims): ask where consumer-side behavior was confirmed. SDKs consume the spec
  via a pinned copy, dashboard/console carry synced mirrors, and closed-source runtime
  components consume orchestrator-side contracts — "no active consumer in this repo" is not
  verification, because the consumer that matters may not live in this repository.
- Accepting a broader input shape (wildcards, prefixes, normalization) for a value another
  component looks up by exact match turns a loud create-time error into a silent runtime
  no-op. Flag the mismatch and ask for the consumer's matching semantics.
- A new migration must be versioned above main's newest; a `NOT NULL` column added to a table
  other systems copy is consumer-breaking until shown otherwise.
- Changes near envd versioning or its wire surface must address already-running sandboxes
  with older envd.
- Nomad `type = "system"` jobs have no `update` stanza, so `progress_deadline` reasoning does
  not apply to them; do not flag `kill_timeout` against it.
- Do not post claims about the outside world (a release exists, an upstream tool rejects a
  config) without stating how they were verified.
- Before requesting tests, check the file's recent PR history — do not re-request tests a
  maintainer deliberately removed.
- Terraform modules consumed via release-please tag refs are pinned by design; never flag tag
  pins as mutable-ref issues.
