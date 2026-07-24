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
# os-release is the image's DECLARED identity (ADR-010) — never probe for
# package managers. Images without it (pure-Nix, distroless, scratch) are
# rejected with a message naming the real problem; supporting them needs the
# explicit distro-declaration override (qa.md QA3), not guessing.
if [ -r /etc/os-release ]; then
    . /etc/os-release
    E2B_DISTRO_ID="${ID:-unknown}"
else
    E2B_DISTRO_ID="unknown (image has no /etc/os-release)"
fi

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
{
    # Prefer the hypervisor's PTP clock (kvm-ptp): no network dependency and
    # it tracks the host directly. It is missing where nested virtualization
    # can't expose it (e.g. dev slots) — chronyd treats a missing PHC as a
    # FATAL error, so only reference it when the device exists and fall back
    # to NTP otherwise; a running chronyd without PHC beats a dead one.
    # Device presence is probed in the provisioning VM, which runs on the same
    # host/KVM as the runtime sandboxes.
    if [ -e /dev/ptp0 ]; then
        echo "refclock PHC /dev/ptp0 poll 2 dpoll 2"
    else
        echo "pool pool.ntp.org iburst maxsources 3"
    fi
    # Step (jump) the clock instead of slewing when the offset exceeds 1s, but
    # only for the first 3 updates after chronyd starts. chronyd restarts on
    # every cold boot/reboot, so this corrects a large boot-time offset fast
    # (TLS needs a correct clock) without risking a backward jump under a
    # running workload. Needed because chrony-wait is masked, so boot no
    # longer blocks on first sync.
    echo "makestep 1.0 3"
} >/etc/chrony/chrony.conf

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

# Init-system-specific boot arrangement: service autostarts (envd, time sync),
# boot-noise silencing, and — on OpenRC — replacing the one-shot provisioning
# inittab with the real boot sequence. The body is rendered per profile family
# by the distro selector (see distro/init.go); everything below stays shared.
e2b_init_setup

# Clean machine-id from Docker
rm -rf /etc/machine-id

echo "Linking $E2B_INIT_BIN to init"
ln -sf "$E2B_INIT_BIN" /usr/sbin/init
# /sbin is a real directory on non-usr-merged distros (Alpine) where the line
# above doesn't reach the /sbin/init the kernel is pointed at; link it too.
[ -L /sbin ] || ln -sf "$E2B_INIT_BIN" /sbin/init

echo "Unlocking immutable configuration"
$BUSYBOX chattr -i /etc/resolv.conf

echo "Finished provisioning script"

# Delete itself
rm -rf /etc/init.d/rcS
rm -rf /usr/local/bin/provision.sh

# Report successful provisioning
printf "0" > "$RESULT_PATH"
