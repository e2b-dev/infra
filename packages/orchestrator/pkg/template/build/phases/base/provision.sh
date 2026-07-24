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

# Identify the base image by its DECLARED /etc/os-release ID (FEAT-145 / ADR-010)
# — not by probing which package manager exists. The selector below is generated
# from the distro profile registry (packages/.../phases/base/distro); it sets
# E2B_PACKAGES, e2b_pkg_query(), e2b_pkg_install(), E2B_INIT_BIN, E2B_TIMESYNC_UNIT,
# E2B_ADMIN_GROUP, E2B_CA_BUNDLE, e2b_ca_refresh() — or exits 1 with a clear error
# on an unsupported distribution.
echo "Detecting base image distribution"
. /etc/os-release 2>/dev/null || true
E2B_DISTRO_ID="${ID:-unknown}"

{{ .DistroSelector }}

echo "Provisioning for distro '$E2B_DISTRO_ID' (init=$E2B_INIT_BIN, timesync=$E2B_TIMESYNC_UNIT, admin-group=$E2B_ADMIN_GROUP)"

# Helper function to check if a package is installed (distro-specific query)
is_package_installed() {
    e2b_pkg_query "$1"
}

# Install required packages if not already installed
PACKAGES="$E2B_PACKAGES"
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
    # shellcheck disable=SC2086
    e2b_pkg_install $MISSING
else
    echo "All required packages are already installed."
fi

# Ensure the system CA trust bundle exists at the path envd expects. On Debian
# the ca-certificates package creates it; on RHEL it is generated under /etc/pki
# by update-ca-trust, so e2b_ca_refresh regenerates/exposes it (FEAT-145).
echo "Ensuring CA trust bundle at $E2B_CA_BUNDLE"
[ -s "$E2B_CA_BUNDLE" ] || e2b_ca_refresh || true

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
# Step (jump) the clock instead of slewing when the offset exceeds 1s, but only
# for the first 3 updates after chronyd starts. chronyd restarts on every cold
# boot/reboot, so this corrects a large boot-time offset fast (TLS needs a
# correct clock) without risking a backward jump under a running workload.
# Needed because chrony-wait is masked, so boot no longer blocks on first sync.
makestep 1.0 3
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

echo "Enable time synchronization ($E2B_TIMESYNC_UNIT)"
# Distro-correct chrony unit (chrony on Debian, chronyd on RHEL/Arch). Replaces
# the previously static, Debian-only chrony.service autostart symlink.
systemctl enable "$E2B_TIMESYNC_UNIT"

echo "Disable chrony-wait"
# chrony-wait blocks multi-user.target until the first clock sync (~8s);
# chrony still syncs in the background, nothing needs to wait for it.
systemctl mask chrony-wait.service 2>/dev/null || true

echo "Disable slow boot units not needed in the sandbox"
# binfmt registrations (foreign-arch exec) take ~1s of CPU early in boot and
# compete with envd start; e2scrub is for LVM-backed ext4 only.
systemctl mask systemd-binfmt.service 2>/dev/null || true
systemctl mask e2scrub_reap.service 2>/dev/null || true

# Clean machine-id from Docker
rm -rf /etc/machine-id

echo "Linking systemd to init"
ln -sf "$E2B_INIT_BIN" /usr/sbin/init

echo "Unlocking immutable configuration"
$BUSYBOX chattr -i /etc/resolv.conf

echo "Finished provisioning script"

# Delete itself
rm -rf /etc/init.d/rcS
rm -rf /usr/local/bin/provision.sh

# Report successful provisioning
printf "0" > "$RESULT_PATH"
