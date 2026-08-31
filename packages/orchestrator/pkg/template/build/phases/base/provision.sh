#!/bin/sh
set -eu

BUSYBOX="{{ .BusyBox }}"
RESULT_PATH="{{ .ResultPath }}"

echo "Starting provisioning script"

# Configure DNS before any network use (align with e2b_val: static resolv.conf so apt/system work).
# Remove symlink if present (e.g. systemd-resolved); write static nameservers.
if [ -L /etc/resolv.conf ]; then
    rm -f /etc/resolv.conf
fi
cat > /etc/resolv.conf <<EOF
nameserver 8.8.8.8
nameserver 8.8.4.4
EOF
# Prevent systemd-resolved from taking over resolv.conf
if [ -f /etc/systemd/resolved.conf ]; then
    if ! grep -q "^DNSStubListener=" /etc/systemd/resolved.conf 2>/dev/null; then
        if grep -q "^\[Resolve\]" /etc/systemd/resolved.conf; then
            sed -i '/^\[Resolve\]/a DNSStubListener=no' /etc/systemd/resolved.conf 2>/dev/null || true
        else
            echo -e "\n[Resolve]\nDNSStubListener=no" >> /etc/systemd/resolved.conf
        fi
    else
        sed -i 's/^DNSStubListener=.*/DNSStubListener=no/' /etc/systemd/resolved.conf 2>/dev/null || true
    fi
fi
mkdir -p /etc/systemd/resolved.conf.d/
cat > /etc/systemd/resolved.conf.d/dns.conf <<EOF
[Resolve]
DNS=8.8.8.8 8.8.4.4
FallbackDNS=
Domains=
DNSSEC=no
EOF

echo "Making configuration immutable"
$BUSYBOX chattr +i /etc/resolv.conf

# Detect the package manager so we can support both Alpine (apk) and Debian/Ubuntu (apt).
if command -v apk >/dev/null 2>&1; then
    PKG_MGR="apk"
elif command -v apt-get >/dev/null 2>&1; then
    PKG_MGR="apt"
else
    PKG_MGR="unknown"
fi
echo "Detected package manager: $PKG_MGR"

if [ "$PKG_MGR" = "apk" ]; then
    # -------- Alpine branch --------
    # Alpine uses OpenRC/busybox-init instead of systemd. Install the userland
    # that envd and the sandbox rely on.
    ALPINE_PACKAGES="openrc openssh-server sudo chrony socat curl ca-certificates fuse3 iptables git nfs-utils less jq bash util-linux shadow"
    echo "Installing Alpine packages: $ALPINE_PACKAGES"

    apk update || {
        echo "E: apk update failed (no outbound internet from build VM). On the HOST: enable ip_forward, NAT/MASQUERADE for 169.254.0.0/30, or configure HTTP_PROXY for the build."
        exit 1
    }
    apk add --no-cache $ALPINE_PACKAGES

    # e2b injects a glibc busybox at /usr/bin/busybox; on Alpine that binary is
    # incompatible with the musl userland, so point it back at the native one.
    ln -sf /bin/busybox /usr/bin/busybox
    # Some tooling expects ionice/nice on PATH; back them with busybox applets.
    [ -e /usr/bin/ionice ] || ln -sf /bin/busybox /usr/bin/ionice
    [ -e /usr/bin/nice ] || ln -sf /bin/busybox /usr/bin/nice

    # Alpine has no "sudo" group by default; create it and grant NOPASSWD sudo.
    addgroup sudo 2>/dev/null || true
    mkdir -p /etc/sudoers.d
    echo '%sudo ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/sudo
    chmod 0440 /etc/sudoers.d/sudo

    # OpenRC service so the sandbox runtime starts envd on boot.
    cat > /etc/init.d/envd <<'EOF'
#!/sbin/openrc-run
name="envd"
description="e2b envd daemon"
command="/usr/bin/envd"
command_background=true
pidfile="/run/envd.pid"

depend() {
    need net
    after firewall
}
EOF
    chmod 0755 /etc/init.d/envd
    mkdir -p /etc/runlevels/default
    ln -sf /etc/init.d/envd /etc/runlevels/default/envd

    # Replace inittab so busybox init mounts the core filesystems and respawns envd.
    cat > /etc/inittab <<'EOF'
::sysinit:/bin/mount -t proc proc /proc
::sysinit:/bin/mount -t sysfs sysfs /sys
::sysinit:/bin/mount -t devtmpfs devtmpfs /dev
::sysinit:/bin/mkdir -p /dev/pts
::sysinit:/bin/mount -t devpts devpts /dev/pts
::sysinit:/bin/sh -c '[ -e /dev/ptmx ] || mknod /dev/ptmx c 5 2'
::respawn:/usr/bin/envd
EOF
else
    # -------- Debian/Ubuntu branch --------
    # Helper function to check if a package is installed
    is_package_installed() {
        dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed"
    }

    # Install required packages if not already installed
    PACKAGES="systemd systemd-sysv openssh-server sudo chrony socat curl ca-certificates fuse3 iptables git nfs-common less nftables iputils-ping jq"
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

        apt-get -q update || {
            echo "E: apt-get update failed (no outbound internet from build VM). On the HOST: enable ip_forward, NAT/MASQUERADE for 169.254.0.0/30, or configure HTTP_PROXY for the build."
            exit 1
        }
        DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get -qq -o=Dpkg::Use-Pty=0 install -y --no-install-recommends $MISSING
        # After installing systemd, resolv.conf may have become a symlink again; restore static DNS.
        if [ -L /etc/resolv.conf ]; then
            $BUSYBOX chattr -i /etc/resolv.conf 2>/dev/null || true
            rm -f /etc/resolv.conf
            cat > /etc/resolv.conf <<EOF
nameserver 8.8.8.8
nameserver 8.8.4.4
EOF
        fi
    else
        echo "All required packages are already installed."
    fi

    # Set /dev/fuse permissions to 666 for non-root access
    # Use systemd-tmpfiles to set permissions at boot
    mkdir -p /etc/tmpfiles.d
    echo 'z /dev/fuse 0666 root root -' > /etc/tmpfiles.d/fuse.conf
fi

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
# systemctl-based tweaks only apply to systemd distros; Alpine (OpenRC) skips them.
if command -v systemctl >/dev/null 2>&1; then
    # This is required when the Firecracker kernel args has specified console=ttyS0
    systemctl mask serial-getty@ttyS0.service

    echo "Disable network online wait"
    systemctl mask systemd-networkd-wait-online.service

    echo "Disable system first boot wizard"
    # This was problem with Ubuntu 24.04, that differently calculate wizard should be called
    # and Linux boot was stuck in wizard until envd wait timeout
    systemctl mask systemd-firstboot.service

    echo "Disable chrony-wait"
    # chrony-wait blocks multi-user.target until the first clock sync (~8s);
    # chrony still syncs in the background, nothing needs to wait for it.
    systemctl mask chrony-wait.service

    echo "Disable slow boot units not needed in the sandbox"
    # binfmt registrations (foreign-arch exec) take ~1s of CPU early in boot and
    # compete with envd start; e2scrub is for LVM-backed ext4 only.
    systemctl mask systemd-binfmt.service
    systemctl mask e2scrub_reap.service
fi

# Clean machine-id from Docker
rm -rf /etc/machine-id

echo "Linking init"
# Prefer systemd when present, then OpenRC (Alpine), otherwise keep the existing init.
if [ -x /lib/systemd/systemd ]; then
    ln -sf /lib/systemd/systemd /usr/sbin/init
elif command -v openrc-init >/dev/null 2>&1; then
    ln -sf "$(command -v openrc-init)" /sbin/init
else
    echo "No systemd or openrc-init found, keeping existing /sbin/init"
fi

echo "Unlocking immutable configuration"
$BUSYBOX chattr -i /etc/resolv.conf

echo "Finished provisioning script"

# Delete itself
rm -rf /etc/init.d/rcS
rm -rf /usr/local/bin/provision.sh

# Report successful provisioning
printf "0" > "$RESULT_PATH"