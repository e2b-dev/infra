#!/bin/bash
# inspired by https://github.com/firecracker-microvm/firecracker/blob/main/resources/rebuild.sh

set -euo pipefail

function install_dependencies {
    apt update
    apt install -y bc flex bison gcc make libelf-dev libssl-dev squashfs-tools busybox-static tree cpio curl patch
}

function get_tag {
    local KERNEL_VERSION=$1

    # list all tags from newest to oldest
    (git --no-pager tag -l --sort=-creatordate | grep microvm-kernel-$KERNEL_VERSION\..*\.amzn2 \
        || git --no-pager tag -l --sort=-creatordate | grep kernel-$KERNEL_VERSION\..*\.amzn2) | head -n1
}

function build_version {
  local version=$1
  echo "Starting build for kernel version: $version"

  cp ../configs/"${version}.config" .config

  echo "Checking out repo for kernel at version: $version"
  git checkout "$(get_tag "$version")"

  echo "Building kernel version: $version"
  make olddefconfig
  make vmlinux -j "$(nproc)"

  echo "Copying finished build to builds directory"
  mkdir -p "../builds/vmlinux-${version}"
  cp vmlinux "../builds/vmlinux-${version}/vmlinux.bin"
}

echo "Cloning the linux kernel repository"

install_dependencies

[ -d linux ] || git clone --no-checkout --filter=tree:0 https://github.com/amazonlinux/linux
pushd linux

make distclean || true

grep -v '^ *#' <../kernel_versions.txt | while IFS= read -r version; do
  build_version "$version"
done

popd
