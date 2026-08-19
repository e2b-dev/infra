# fc-kernels

## Overview

This project builds custom Linux kernels for Firecracker microVMs from the same kernel sources as the official Firecracker repo, using the configuration files (and optional patches) that live in this repo.

## Prerequisites

- Linux environment (for building kernels)

## Building locally

1. **Configure kernel versions:**
   - Edit `kernel_versions.txt` to specify which kernel versions to build (one per line, e.g. `6.1.158`).
   - Place the corresponding config(s) in `configs/x86_64/<version>.config` and `configs/arm64/<version>.config`.
   - (Optional) Drop `*.patch` files into `patches/<version>/` to apply on top of the upstream tree before build.

2. **Build:**
   ```sh
   make build              # builds all versions in kernel_versions.txt for x86_64
   make build-arm64        # same, for arm64
   ./build.sh 6.1.158      # build a single version (x86_64)
   ./build.sh 6.1.158 arm64
   ```

   Output: `builds/vmlinux-<version>/<arch>/vmlinux.bin` where `<arch>` is `amd64` or `arm64` (Go/OCI convention). For x86_64 a legacy copy is also placed at `builds/vmlinux-<version>/vmlinux.bin`.

## Adding a patch release

A version is built from the newest `microvm-kernel*<version>-*.amzn*` tag of [amazonlinux/linux](https://github.com/amazonlinux/linux), so that is where the available patch releases come from. Match the prefix loosely: upstream puts the series there on some tags (`microvm-kernel-6.1.158-15.288.amzn2023`, but `microvm-kernel6.18-6.18.36-71.136.amzn2023`), and a pattern that assumes the hyphen quietly hides the newest releases of a series.

```sh
git ls-remote --tags https://github.com/amazonlinux/linux | grep -E 'microvm-kernel[^ ]*-6\.1\.[0-9]+-'
```

A new version needs its own config, seeded from the version it supersedes and then settled against the tree it will be built from:

```sh
for arch in x86_64 arm64; do cp configs/$arch/6.1.158.config configs/$arch/6.1.177.config; done
cp -r patches/6.1.158 patches/6.1.177          # only if that version carries patches
sudo env CONFIG_ONLY=1 ./build.sh 6.1.177      # root: build.sh installs its build dependencies
sudo env CONFIG_ONLY=1 ./build.sh 6.1.177 arm64
sudo chown -R "$(id -u):$(id -g)" configs      # the resolved configs come back root-owned
```

`CONFIG_ONLY=1` stops after `olddefconfig` and writes the resolved config back to `configs/<arch>/<version>.config`, so the symbols the newer tree added are recorded and reviewable rather than silently taking upstream defaults at build time. It applies `patches/<version>/` on the way, which is where a carried patch that no longer applies shows up. Run it per architecture on that architecture's machine: Kconfig probes the compiler, so a config resolved cross is not the one a native build resolves.

Then add the version to `kernel_versions.txt` and build.

Every pinned version is rebuilt on every release, so the list cannot grow forever. A pin whose comment says `candidate` is one nothing has taken into use yet, and is the one the next version supersedes; adopting a version means deleting that comment. Published artifacts are never overwritten, so dropping a pin does not remove the kernels already in a bucket.

## New kernel in E2B's infra
_Note: these steps should give you a new kernel on your self-hosted E2B using https://github.com/e2b-dev/infra_

- Build the kernel from the new config or patch.
- Update `DefaultKernelVersion` in [packages/api/internal/cfg/model.go](https://github.com/e2b-dev/infra/blob/main/packages/api/internal/cfg/model.go) if you changed the kernel version.
- Build and deploy `api`.

## Architecture naming

Output directories use Go's `runtime.GOARCH` convention (`amd64`, `arm64`) so they match the infra orchestrator's `TargetArch()` path resolution. The build-time variable `TARGET_ARCH` (`x86_64`, `arm64`) is only used internally for config paths and cross-compilation flags.

## License

This project is licensed under the Apache License 2.0. See [LICENSE](../../LICENSE) for details.
