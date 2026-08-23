# fc-versions

## Overview

This project builds custom Firecracker versions for E2B sandbox VMs.

## Prerequisites

- Linux environment (for building firecracker)
- `FIRECRACKER_REPO_TOKEN` set, to clone the Firecracker source

## Build

```sh
./build.sh <commit_hash> <version_name> [arch]
```

`arch` is `amd64` (the default) or `arm64`. Run the script from this directory. It resolves its output through a relative path.

Output: `builds/<version_name>/<arch>/firecracker`

## Releases

A release is named by the tag it is built from, `vX.Y-<e2b-semver>`: the
upstream minor line we track, then our own version of the patches carried on
top of it (for example `v1.14-0.1.0`). The tag is cut by hand on the fork
before the release is built — the pipeline resolves it and refuses anything
else, so it never composes a release name of its own.

Releases named `<tag>_<sha>` predate this scheme and are never rebuilt.

## License

This project is licensed under the Apache License 2.0. See [LICENSE](../../LICENSE) for details.
