# E2B premade NixOS base image

NixOS templates work the inverse of every other family: instead of the
orchestrator provisioning the image imperatively, the image is **premade** from
`configuration.nix`, which declares everything `provision.sh` installs
elsewhere — the envd systemd unit, chrony, sshd, the default `user` (with a
matching `user` group and the exact sudoers line the build steps check for),
`/bin/bash` (build steps invoke it explicitly), and the journald watchdog
override. The orchestrator's `nixos` profile then only verifies and boots
(see `../distro.go` and the `InitNixOS` block in `../init.go`).

## Building and publishing

`./build.sh <tag> [registry]` (run on a Linux host with docker; the registry
defaults to `127.0.0.1:5000`):

1. evaluates the NixOS system closure with `nix` inside a `nixos/nix`
   container, against the exact `nixpkgs` revision pinned in the script (see
   [nixpkgs pin](#nixpkgs-pin)), from the `configuration.nix` committed next
   to the script,
2. packs the closure into a single-layer OCI rootfs tar, adding the three
   pieces of glue the boot path needs:
   - `/sbin/init -> /nix/var/nix/profiles/system/init` (the stage-2 init the
     `nixos` profile points the kernel at),
   - `/nix/var/nix/profiles/system -> <toplevel store path>`,
   - a static `/etc/os-release` with `ID=nixos` so the distro selector can
     identify the image *before* the first activation generates the real one,
3. `docker import`s and pushes the tar.

**Push every rebuild under a NEW TAG** (hence the required `<tag>` argument).
The base-layer cache key includes the image reference as written in the
Dockerfile — republishing under the same tag silently reuses the previously
cached base layer (observed; same "default tag" ambiguity called out in
`phases/base/hash.go`).

## nixpkgs pin

`build.sh` pins an exact nixpkgs revision:

| | |
|---|---|
| `NIXPKGS_REV` | `21ea275a7c46aef9d4d6ddc962e6d562e9d94183` |
| release | `nixos-26.05.6503`, 2026-07-30 |

It is a revision and not `channel:nixos-XX.YY` on purpose. A channel resolves
at build time, so the same commit would evaluate to a different closure on
every run — the image could not be reproduced or bisected, and "what changed"
would have no answer. `system.stateVersion` in `configuration.nix` tracks this
pin.

**Bumping it is how the image gets security updates**, and it is the whole
maintenance story for this file: pick a revision from a release branch
upstream still maintains, update `NIXPKGS_REV`/`NIXPKGS_RELEASE` and
`stateVersion`, rebuild, and re-run the boot checks — the boot glue
(`/sbin/init` chain, the systemd `/etc` symlink handling in `../init.go`) is
the part that breaks across releases, and a tar-level check will not catch it.

Do not let the pin drift onto an unmaintained branch. NixOS releases get about
seven months of support; `nixos-24.05`, which this image used until it was
moved to 26.05, stopped receiving commits on 2024-12-30.

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
