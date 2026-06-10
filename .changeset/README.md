# Changesets

This folder holds pending release notes, managed with
[changesets](https://github.com/changesets/changesets). It currently versions
a single package: **envd** (`packages/envd`).

Every PR that touches `packages/envd/` must include a changeset — CI enforces
this. Create one with:

```bash
npm install
npx changeset
```

Pick the bump (`major` = breaking, `minor` = feature, `patch` = fix) and write
a short, user-facing description — it becomes the entry in
`packages/envd/CHANGELOG.md`.

If the change cannot affect the compiled envd binary (docs, comments, dev
tooling), add an empty changeset instead:

```bash
npx changeset --empty
```

On merge to `main`, the `envd-release` workflow consumes all pending
changesets: it bumps `packages/envd/package.json` by the highest requested
bump, syncs the version into `packages/envd/pkg/version.go` (via
`scripts/sync-envd-version.sh`), updates `packages/envd/CHANGELOG.md`, and
commits the release back to main. Don't bump versions manually.
