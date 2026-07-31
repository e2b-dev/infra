# E2B premade NixOS base image

NixOS templates work the inverse of every other family: instead of the
orchestrator provisioning the image imperatively, the image is **premade** from
`configuration.nix`, which declares everything `provision.sh` installs
elsewhere — the envd systemd unit, chrony, sshd, the default `user` (with a
matching `user` group and the exact sudoers line the build steps check for),
`/bin/bash` (build steps invoke it explicitly), and the journald watchdog
override. The orchestrator's `nixos` profile then only verifies and boots
(see `../distro.go` and the `InitNixOS` block in `../init.go`).

## Publishing

Run the **Publish NixOS base image** workflow
(`.github/workflows/nixos-base-image.yml`) from the Actions tab, with:

- `tag` — required, and must be one that was never published (e.g.
  `24.05-20260731`). See the new-tag rule below; the job refuses to overwrite
  an existing tag.
- `namespace` — Docker Hub namespace, defaults to `e2bdev`.

It authenticates with the `DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN` repository
secrets and fails immediately with a message naming them if either is unset.

The workflow runs the same `build.sh` as a local build, verifies the rootfs tar
(`ID=nixos` in `/etc/os-release`, the `/sbin/init` and
`/nix/var/nix/profiles/system` symlinks, the toplevel closure they resolve to,
and a non-empty `/nix/var/nix/db-registration`) and only then pushes, under two
references:

```
docker.io/e2bdev/nixos:<tag>     # immutable, reproducible — use this
docker.io/e2bdev/nixos:latest    # moving pointer at the newest verified build
```

Either is what a template passes to `fromImage`. Both are echoed in the run's
job summary. `latest` is tagged in the push step, *after* the tar checks pass,
so it never names a build that failed verification.

> **Prefer the immutable tag.** `latest` is a convenience pointer only. The
> base-layer cache key is the image reference *as written*, not the resolved
> digest (`phases/base/hash.go`), so a template built `FROM
> e2bdev/nixos:latest` keeps the same cache key after `latest` moves and goes
> on building from the **stale** base layer until it is force-rebuilt. Pin
> `:<tag>` for anything that has to be reproducible.

The first publish creates the `nixos` repository on Docker Hub; check that its
visibility matches the namespace's default (it has to be public for customers
to pull it).

## Building locally

`./build.sh <tag> [registry]` builds `<registry>/nixos:<tag>` (run on a Linux
host with docker; the registry defaults to `127.0.0.1:5000`, so a bare
`./build.sh dev` gives `127.0.0.1:5000/nixos:dev`):

1. evaluates the NixOS system closure with `nix` inside a `nixos/nix`
   container (`nixpkgs` channel pinned in the script), from the
   `configuration.nix` committed next to the script,
2. packs the closure into a single-layer OCI rootfs tar, adding the three
   pieces of glue the boot path needs:
   - `/sbin/init -> /nix/var/nix/profiles/system/init` (the stage-2 init the
     `nixos` profile points the kernel at),
   - `/nix/var/nix/profiles/system -> <toplevel store path>`,
   - a static `/etc/os-release` with `ID=nixos` so the distro selector can
     identify the image *before* the first activation generates the real one,
3. `docker import`s and pushes the tar. `E2B_NIXOS_SKIP_PUSH=1` stops after
   the import, so the tar and the image can be inspected before anything
   reaches a registry — that is how the publish workflow gates its checks, and
   why `latest` is moved by the workflow rather than by this script.

Staging directory: `/var/tmp/e2b-nixos-base`, overridable with
`E2B_NIXOS_WORKDIR`.

**Push every rebuild under a NEW TAG** (hence the required `<tag>` argument).
The base-layer cache key includes the image reference as written in the
Dockerfile — republishing under the same tag silently reuses the previously
cached base layer (observed; same "default tag" ambiguity called out in
`phases/base/hash.go`). A bad tag therefore cannot be fixed by rebuilding over
it, which is why the workflow verifies the tar before it pushes.

`latest` is the one reference that is deliberately republished, and it pays
exactly that price — which is why it is documented as a pointer and not as a
reproducible reference.

## Boot-path notes (all observed on real KVM)

- Before the first activation the image has **no FHS userland** — no
  `/bin/sh`, no `mkdir`. The provisioning boot runs entirely through the baked
  busybox (see `core/rootfs/files/rcS.sh.tpl`, `inittab.tpl`,
  `provision-runner.sh.tpl`), and the `nixos` profile's `Bootstrap` puts
  busybox applets on `PATH` for the shared provisioning body.
- NixOS activation manages `/etc/systemd/system` as a symlink into the store;
  the baked systemd drop-ins must be removed at provisioning (the `InitNixOS`
  setup does this) or `setup-etc` refuses the symlink and systemd boots with
  no units at all ("Unit default.target not found").
- The sandbox gets the nix toolchain natively (`nix-env` on PATH for `user`).
