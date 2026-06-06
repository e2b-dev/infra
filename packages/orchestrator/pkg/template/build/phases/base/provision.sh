#!/bin/sh
set -eu

BUSYBOX="{{ .BusyBox }}"
RESULT_PATH="{{ .ResultPath }}"

echo "Starting provisioning script"

{{ if eq .Provider "gcp" }}
# GCP Specific logic
{{ end }}

echo "Making configuration immutable"
$BUSYBOX chattr +i /etc/resolv.conf

# Detect the base image's package manager. Only systemd-based distributions are
# supported, because systemd is used as the runtime init. Alpine (apk) and other
# non-systemd distros are rejected here.
RPM_INSTALL=""
if command -v apt-get >/dev/null 2>&1; then
    PKG_FAMILY="debian"
# --allowerasing lets dnf/yum swap conflicting packages (e.g. replace the
# preinstalled curl-minimal with the full curl package on RHEL/Alma) instead of
# failing the whole transaction.
elif command -v dnf >/dev/null 2>&1; then
    PKG_FAMILY="rhel"; RPM_INSTALL="dnf -y --allowerasing install"
elif command -v yum >/dev/null 2>&1; then
    PKG_FAMILY="rhel"; RPM_INSTALL="yum -y --allowerasing install"
elif command -v microdnf >/dev/null 2>&1; then
    PKG_FAMILY="rhel"; RPM_INSTALL="microdnf -y install"
elif command -v pacman >/dev/null 2>&1; then
    PKG_FAMILY="arch"
else
    echo "ERROR: no supported package manager found (need apt-get, dnf/yum/microdnf, or pacman)."
    echo "Alpine, openSUSE, and other unsupported distributions will fail here."
    exit 1
fi
echo "Detected package family: $PKG_FAMILY"

# Required packages, mapped to each distro's package names. systemd-sysv is
# Debian-only (systemd itself provides init elsewhere); bash is listed so even
# minimal images get it (required by the build's /bin/bash command runner).
# passwd/useradd/usermod (used here and by later build steps) are not always
# present on minimal images: on Debian they come from `passwd`, on RHEL from
# `shadow-utils` + `passwd`, on Arch from `shadow`.
case "$PKG_FAMILY" in
    debian)
        PACKAGES="systemd systemd-sysv passwd openssh-server sudo chrony socat curl ca-certificates fuse3 iptables git nfs-common less nftables iputils-ping jq bash"
        ;;
    rhel)
        # On RHEL 9 the iptables command ships in iptables-nft; the bare
        # "iptables" package no longer exists (it's only a virtual provide), so
        # rpm -q iptables would never match. Use the real package name.
        PACKAGES="systemd shadow-utils passwd openssh-server sudo chrony socat curl ca-certificates fuse3 iptables-nft git nfs-utils less nftables iputils jq bash"
        ;;
    arch)
        PACKAGES="systemd shadow openssh sudo chrony socat curl ca-certificates fuse3 iptables git nfs-utils less nftables iputils jq bash"
        ;;
esac

# Helper function to check if a package is installed
is_package_installed() {
    case "$PKG_FAMILY" in
        debian) dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed" ;;
        rhel) rpm -q "$1" >/dev/null 2>&1 ;;
        arch) pacman -Q "$1" >/dev/null 2>&1 ;;
    esac
}

echo "Checking presence of the following packages: $PACKAGES"

MISSING=""
for pkg in $PACKAGES; do
    if ! is_package_installed "$pkg"; then
        echo "Package $pkg is missing, will install it."
        MISSING="$MISSING $pkg"
    fi
done

if [ -n "$MISSING" ]; then
    echo "Missing packages detected, installing:$MISSING"
    case "$PKG_FAMILY" in
        debian)
            apt-get -q update
            DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get -qq -o=Dpkg::Use-Pty=0 install -y --no-install-recommends $MISSING
            ;;
        rhel)
            $RPM_INSTALL $MISSING
            ;;
        arch)
            pacman -Sy --noconfirm
            pacman -S --noconfirm --needed $MISSING
            ;;
    esac
else
    echo "All required packages are already installed."
fi

# Set /dev/fuse permissions to 666 for non-root access
# Use systemd-tmpfiles to set permissions at boot
mkdir -p /etc/tmpfiles.d
echo 'z /dev/fuse 0666 root root -' > /etc/tmpfiles.d/fuse.conf

echo "Setting up shell"
echo "export SHELL='/bin/bash'" >/etc/profile.d/shell.sh
echo "export PS1='\w \$ '" >/etc/profile.d/prompt.sh
echo "export PS1='\w \$ '" >>"/etc/profile"
echo "export PS1='\w \$ '" >>"/root/.bashrc"

echo "Use .bashrc and .profile"
echo "if [ -f ~/.bashrc ]; then source ~/.bashrc; fi; if [ -f ~/.profile ]; then source ~/.profile; fi" >>/etc/profile

echo "Remove root password"
passwd -d root

echo "Setting up chrony"
mkdir -p /etc/chrony
cat <<EOF >/etc/chrony/chrony.conf
refclock PHC /dev/ptp0 poll 2 dpoll 2
EOF

# Add a proxy config, as some environments expects it there (e.g. timemaster in Node Dockerimage)
echo "include /etc/chrony/chrony.conf" >/etc/chrony.conf

echo "Setting up SSH"
mkdir -p /etc/ssh
cat <<EOF >>/etc/ssh/sshd_config
PermitRootLogin yes
PermitEmptyPasswords yes
PasswordAuthentication yes
EOF

echo "Increasing inotify watch limit"
echo 'fs.inotify.max_user_watches=65536' | tee -a /etc/sysctl.conf

# Disable kcompactd background page migration. With 2 MiB host-side hugepage
# backing of guest RAM, every migration dirties a destination hugepage from
# the host UFFD's perspective and lands in the next memfile diff, with no
# corresponding workload benefit between snapshots. We trigger compaction
# explicitly pre-pause instead.
echo "Disabling proactive memory compaction"
echo 'vm.compaction_proactiveness=0' | tee -a /etc/sysctl.conf

echo "Don't wait for ttyS0 (serial console kernel logs)"
# This is required when the Firecracker kernel args has specified console=ttyS0
systemctl mask serial-getty@ttyS0.service

echo "Disable network online wait"
systemctl mask systemd-networkd-wait-online.service

echo "Disable system first boot wizard"
# This was problem with Ubuntu 24.04, that differently calculate wizard should be called
# and Linux boot was stuck in wizard until envd wait timeout
systemctl mask systemd-firstboot.service

# The chrony service unit name differs across distros (chrony on Debian,
# chronyd on RHEL/Arch). Enable whichever exists; this is the single place
# chrony autostart is configured for every distro family.
echo "Enabling time synchronization service"
systemctl enable chronyd 2>/dev/null || systemctl enable chrony 2>/dev/null || true

# Ensure /etc/ssl/certs/ca-certificates.crt exists so envd's CA injection and
# TLS clients work. Debian's ca-certificates package creates it on install; on
# RHEL the bundle is generated via update-ca-trust under /etc/pki, so expose it
# under the Debian-style path envd expects. (Arch creates this path natively.)
echo "Ensuring CA certificate bundle exists"
if [ ! -s /etc/ssl/certs/ca-certificates.crt ]; then
    if command -v update-ca-certificates >/dev/null 2>&1; then
        update-ca-certificates || true
    fi
    if [ ! -s /etc/ssl/certs/ca-certificates.crt ] && command -v update-ca-trust >/dev/null 2>&1; then
        update-ca-trust extract || true
    fi
    # If the canonical path still doesn't exist, symlink it to the bundle that
    # update-ca-trust generated under /etc/pki (RHEL).
    if [ ! -s /etc/ssl/certs/ca-certificates.crt ] && [ -s /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem ]; then
        mkdir -p /etc/ssl/certs
        ln -sf /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem /etc/ssl/certs/ca-certificates.crt
    fi
fi

# Clean machine-id from Docker
rm -rf /etc/machine-id

echo "Linking systemd to init"
SYSTEMD_BIN=""
for sd in /lib/systemd/systemd /usr/lib/systemd/systemd; do
    if [ -x "$sd" ]; then
        SYSTEMD_BIN="$sd"
        break
    fi
done
if [ -z "$SYSTEMD_BIN" ]; then
    echo "ERROR: systemd binary not found; base image must provide systemd."
    exit 1
fi
ln -sf "$SYSTEMD_BIN" /usr/sbin/init

echo "Unlocking immutable configuration"
$BUSYBOX chattr -i /etc/resolv.conf

echo "Finished provisioning script"

# Delete itself
rm -rf /etc/init.d/rcS
rm -rf /usr/local/bin/provision.sh

# Report successful provisioning
printf "0" > "$RESULT_PATH"
