# Releasing packages to e2b-artifacts

This repository is a read-only mirror: its source of truth is E2B's internal
monorepo, which exports the code here via copybara. Releases are cut there
too — the [release-please](https://github.com/googleapis/release-please)
config, the per-package release PRs, the `<component>-v<version>` git tags,
and the publish workflow all live in the monorepo (each package's directory
here maps to `go/oss/<directory>` there). No release tags or GitHub Releases
appear in this repository.

| Component | Package directory | Published artifact |
|---|---|---|
| api | `packages/api` | images `us-docker.pkg.dev/e2b-artifacts/api/api` and `…/api/db-migrator` (released as one unit) |
| client-proxy | `packages/client-proxy` | image `us-docker.pkg.dev/e2b-artifacts/client-proxy/client-proxy` |
| clickhouse-migrator | `packages/clickhouse` | image `us-docker.pkg.dev/e2b-artifacts/clickhouse-migrator/clickhouse-migrator` |
| dashboard-api | `packages/dashboard-api` | image `us-docker.pkg.dev/e2b-artifacts/dashboard-api/dashboard-api` |
| envd | `packages/envd` | binary `https://storage.googleapis.com/e2b-artifact-binaries/envd/v<version>/envd` |
| nomad-nodepool-apm | `packages/nomad-nodepool-apm` | binaries `nomad-nodepool-apm`, `nomad-deployment-aware-target` under `…/nomad-nodepool-apm/v<version>/` |
| orchestrator | `packages/orchestrator` | binaries `orchestrator`, `clean-nfs-cache` under `…/orchestrator/v<version>/` |

## How a release happens

1. Conventional commits (`feat:`, `fix:`) touching a package's directory in
   the monorepo accumulate into a per-package release PR maintained by the
   monorepo's Release Please workflow. (The same commits arrive here through
   the copybara export, so this mirror's history shows what each release
   contains.)
2. Merging the release PR tags the merge commit `<component>-v<version>` in
   the monorepo (a git tag only — no GitHub Release is created).
3. The tag push triggers the monorepo's publish workflow, which builds the
   artifact from the same sources this mirror shows and pushes it to
   `e2b-artifacts` — images as `:v<version>`, binaries as versioned objects
   in the public `e2b-artifact-binaries` bucket.

Ordinary merges never publish: only a release PR merge (or a manual tag,
below) mints a tag.

## Release candidates / manual publishes

Push a `<component>-v<version>` tag by hand in the monorepo (e.g.
`client-proxy-v2.0.0-rc1`) and the publish workflow publishes that commit as
`:v2.0.0-rc1`. The registry has immutable tags and the binaries bucket is
create-only, so a manual tag can never overwrite an existing version — a bad
published version is fixed by cutting the next version, never by
re-publishing.

## Recovery

All recovery happens in the monorepo:

- **Publish failed**: re-run that tag's publish run (any time), or dispatch
  the publish workflow manually with the tag name.
- **Release merged but never tagged** (e.g. the merge wasn't the head commit
  of its push): push the `<component>-v<version>` tag on the merge commit by
  hand, and swap the release PR's label from `autorelease: pending` to
  `autorelease: tagged` — otherwise release-please refuses to open new
  release PRs for any package.
