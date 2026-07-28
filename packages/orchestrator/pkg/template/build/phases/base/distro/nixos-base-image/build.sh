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

# Build the toplevel closure with nix inside the nixos/nix container.
docker run --rm -v "$work:/build" nixos/nix:latest sh -c "
set -e
nix-build -I nixpkgs=channel:nixos-24.05 -I nixos-config=/build/configuration.nix \
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
cat > \$staging/etc/os-release <<OSR
NAME=NixOS
ID=nixos
VERSION_ID=\"24.05\"
PRETTY_NAME=\"NixOS 24.05 (E2B sandbox base)\"
OSR
tar -rf /build/nixos-rootfs.tar -C \$staging sbin etc nix
echo PACKED
"
ls -lh "$work/nixos-rootfs.tar"
docker import "$work/nixos-rootfs.tar" "$image"
docker push "$image"
echo "IMAGE_PUSHED $image"
