# Releasing packages to e2b-artifacts

Some packages are versioned with [release-please](https://github.com/googleapis/release-please)
(`release-please-config.json` / `.release-please-manifest.json`):

| Component | Package directory | Image |
|---|---|---|
| docker-reverse-proxy | `packages/docker-reverse-proxy` | `us-docker.pkg.dev/e2b-artifacts/docker-reverse-proxy/docker-reverse-proxy` |
| client-proxy | `packages/client-proxy` | `us-docker.pkg.dev/e2b-artifacts/client-proxy/client-proxy` |
| clickhouse-migrator | `packages/clickhouse` | `us-docker.pkg.dev/e2b-artifacts/clickhouse-migrator/clickhouse-migrator` |

## How a release happens

1. Conventional commits (`feat:`, `fix:`) touching a package accumulate into a
   per-package release PR maintained by `.github/workflows/release-please.yml`.
2. Merging the release PR makes that workflow tag the merge commit
   `<component>-v<version>` (a git tag only — no GitHub Release is created) and
   swap the release PR's label to `autorelease: tagged`.
3. The tag push triggers `.github/workflows/publish.yml`, which builds the
   image at that commit and pushes it to `e2b-artifacts` as `:v<version>`.

Ordinary merges to `main` never publish: only a release PR merge (or a manual
tag, below) mints a tag.

## Release candidates / manual publishes

Push a `<component>-v<version>` tag by hand (e.g.
`client-proxy-v2.0.0-rc1`) and `publish.yml` publishes that commit as
`:v2.0.0-rc1`. The registry has immutable tags, so a manual tag can never
overwrite an existing version — a bad published version is fixed by cutting the
next version, never by re-publishing.

## Recovery

- **Publish failed**: re-run that tag's `publish.yml` run (any time), or
  dispatch `publish.yml` manually with the tag name.
- **Release merged but never tagged** (e.g. the merge wasn't the head commit
  of its push): push the `<component>-v<version>` tag on the merge commit by
  hand, and swap the release PR's label from `autorelease: pending` to
  `autorelease: tagged` — otherwise release-please refuses to open new release
  PRs for any package.
