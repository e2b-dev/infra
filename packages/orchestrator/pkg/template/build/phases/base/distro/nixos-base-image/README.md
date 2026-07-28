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
   container (`nixpkgs` channel pinned in the script), from the
   `configuration.nix` committed next to the script,
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
