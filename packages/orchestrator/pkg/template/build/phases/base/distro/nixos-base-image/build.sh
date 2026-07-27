#!/bin/bash
set -e
cd /root/nixos-e2b
# Build the toplevel closure with nix inside the nixos/nix container.
docker run --rm -v /root/nixos-e2b:/build nixos/nix:latest sh -c "
set -e
nix-build -I nixpkgs=channel:nixos-24.05 -I nixos-config=/build/configuration.nix \
  '<nixpkgs/nixos>' -A config.system.build.toplevel -o /build/result
top=\$(readlink /build/result)
echo \"TOPLEVEL=\$top\"
# Pack the full closure + the boot/identity glue into one rootfs tar.
nix-store -qR /build/result > /build/closure.txt
tar -cf /build/nixos-rootfs.tar \$(cat /build/closure.txt)
staging=/tmp/extra
mkdir -p \$staging/sbin \$staging/etc \$staging/nix/var/nix/profiles
ln -s \$top \$staging/nix/var/nix/profiles/system
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
ls -lh /root/nixos-e2b/nixos-rootfs.tar
docker import /root/nixos-e2b/nixos-rootfs.tar 127.0.0.1:5000/e2b-nixos:latest
docker push 127.0.0.1:5000/e2b-nixos:latest
echo IMAGE_PUSHED
