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

## License

This project is licensed under the Apache License 2.0. See [LICENSE](../../LICENSE) for details.
