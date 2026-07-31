#!/bin/bash
# Build and publish the E2B premade NixOS base image.
#
#   ./build.sh <tag> [registry]
#
# Runs from its own directory so the configuration.nix committed next to it is
# the one that gets built. The tag is required: the base-layer cache key
# includes the image reference as written in the Dockerfile, so republishing
# under a tag that was already built silently reuses the cached base layer.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
tag=${1:-}
registry=${2:-127.0.0.1:5000}
if [ -z "$tag" ]; then
  echo "usage: ${BASH_SOURCE[0]} <tag> [registry]   # push every rebuild under a NEW tag" >&2
  exit 1
fi
image="$registry/e2b-nixos:$tag"

# Staged outside the repo: the closure tar is ~700 MB, and the repo checkout can
# be a slow network mount on a dev box.
work=${E2B_NIXOS_WORKDIR:-/var/tmp/e2b-nixos-base}
mkdir -p "$work"
cp "$here/configuration.nix" "$work/configuration.nix"
rm -f "$work/result"

# nixpkgs is pinned to one exact channel release, not to a channel name.
# `channel:nixos-XX.YY` resolves at build time, so the same commit would
# evaluate to a different closure on every run — the image could not be
# reproduced or bisected. The release URL is immutable (it carries the release
# id), so this is a real pin while still being the artifact the channel serves.
#
# Bumping these three is the whole maintenance story, and is how the image
# picks up security updates. Keep the series on a release upstream still
# maintains: NixOS releases get ~7 months, and nixos-24.05 (used here until
# this pin) stopped receiving commits on 2024-12-30.
NIXOS_SERIES=${NIXOS_SERIES:-26.05}
NIXPKGS_RELEASE=${NIXPKGS_RELEASE:-nixos-26.05.6503.21ea275a7c46}
NIXPKGS_URL=${NIXPKGS_URL:-https://releases.nixos.org/nixos/$NIXOS_SERIES/$NIXPKGS_RELEASE/nixexprs.tar.xz}

# Build the toplevel closure with nix inside the nixos/nix container.
echo "nixpkgs pin: $NIXPKGS_RELEASE"
docker run --rm -v "$work:/build" nixos/nix:2.35.1 sh -c "
set -e
nix-build -I nixpkgs=$NIXPKGS_URL -I nixos-config=/build/configuration.nix \
  '<nixpkgs/nixos>' -A config.system.build.toplevel -o /build/result
top=\$(readlink /build/result)
echo \"TOPLEVEL=\$top\"
# Pack the full closure + the boot/identity glue into one rootfs tar.
nix-store -qR /build/result > /build/closure.txt
tar -cf /build/nixos-rootfs.tar \$(cat /build/closure.txt)
staging=/tmp/extra
rm -rf \$staging
mkdir -p \$staging/sbin \$staging/etc \$staging/nix/var/nix/profiles
ln -s \$top \$staging/nix/var/nix/profiles/system
# Tarring store paths does not make them valid to nix: the DB lives in
# /nix/var/nix/db, which the closure does not carry. Ship the registration so
# first boot can load it, otherwise nix-env and friends reject every path.
nix-store --dump-db \$(cat /build/closure.txt) > \$staging/nix/var/nix/db-registration
ln -s /nix/var/nix/profiles/system/init \$staging/sbin/init
# Derived from NIXOS_SERIES so it cannot drift from the pin above: this is the
# pre-activation os-release, and the only field the distro selector reads is
# ID, so a stale version here is invisible to every automated check.
cat > \$staging/etc/os-release <<OSR
NAME=NixOS
ID=nixos
VERSION_ID=\"$NIXOS_SERIES\"
PRETTY_NAME=\"NixOS $NIXOS_SERIES (E2B sandbox base)\"
OSR
tar -rf /build/nixos-rootfs.tar -C \$staging sbin etc nix
echo PACKED
"
ls -lh "$work/nixos-rootfs.tar"
docker import "$work/nixos-rootfs.tar" "$image"
docker push "$image"
echo "IMAGE_PUSHED $image"
