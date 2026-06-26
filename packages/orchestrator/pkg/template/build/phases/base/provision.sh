#!/bin/sh
set -eu

BUSYBOX="{{ .BusyBox }}"
RESULT_PATH="{{ .ResultPath }}"

echo "Starting provisioning script"

{{ if eq .Provider "gcp" }}
# GCP Specific logic
{{ end }}

# Detect the distro family
detect_distro() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        case "$ID" in
            alpine)                    echo "alpine" ;;
            centos|rhel|rocky|alma)    echo "rhel" ;;
            fedora)                    echo "rhel" ;;
            debian|ubuntu|linuxmint)   echo "debian" ;;
            *)                         echo "unknown" ;;
        esac
    elif [ -f /etc/centos-release ] || [ -f /etc/redhat-release ]; then
        echo "rhel"
    elif [ -f /etc/alpine-release ]; then
        echo "alpine"
    else
        echo "unknown"
    fi
}

DISTRO=$(detect_distro)
echo "Detected distro family: $DISTRO"

echo "Making configuration immutable"
$BUSYBOX chattr +i /etc/resolv.conf

# --- Package installation per distro ---
install_packages_debian() {
    is_pkg_installed() {
        dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed"
    }
    PACKAGES="systemd systemd-sysv openssh-server sudo chrony socat curl ca-certificates iptables git nfs-common less nftables iputils-ping jq"
    echo "Checking presence of the following packages: $PACKAGES"
    MISSING=""
    for pkg in $PACKAGES; do
        if ! is_pkg_installed "$pkg"; then
            echo "Package $pkg is missing, will install it."
            MISSING="$MISSING $pkg"
        fi
    done

    # Handle EOL releases by switching to archive mirrors
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        APT_OUTPUT=$(apt-get -q update 2>&1) && APT_RC=0 || APT_RC=$?
        echo "$APT_OUTPUT"
        # Some old apt versions return 0 even when repos are 404, so also check output
        if [ "$APT_RC" -ne 0 ] || echo "$APT_OUTPUT" | grep -qE "does not have a Release file|Failed to fetch"; then
            echo "apt-get update failed or has repo errors (exit code $APT_RC), attempting EOL mirror fallback for $ID $VERSION_CODENAME"
            if [ "$ID" = "ubuntu" ]; then
                sed -i -e 's|archive.ubuntu.com|old-releases.ubuntu.com|g' \
                       -e 's|security.ubuntu.com|old-releases.ubuntu.com|g' \
                       /etc/apt/sources.list 2>/dev/null || true
            elif [ "$ID" = "debian" ]; then
                # Debian EOL releases (e.g. buster, stretch) are moved to archive.debian.org
                sed -i -e 's|deb.debian.org|archive.debian.org|g' \
                       -e 's|security.debian.org|archive.debian.org|g' \
                       /etc/apt/sources.list 2>/dev/null || true
                # Remove lines with -updates or -backports as they don't exist in archive
                sed -i '/-updates/d; /-backports/d' /etc/apt/sources.list 2>/dev/null || true
                # Also handle /etc/apt/sources.list.d/ if present
                if [ -d /etc/apt/sources.list.d ]; then
                    sed -i -e 's|deb.debian.org|archive.debian.org|g' \
                           -e 's|security.debian.org|archive.debian.org|g' \
                           /etc/apt/sources.list.d/*.list 2>/dev/null || true
                    sed -i '/-updates/d; /-backports/d' /etc/apt/sources.list.d/*.list 2>/dev/null || true
                fi
            fi
            apt-get -q update
        fi
    else
        apt-get -q update
    fi

    # fuse3 is not available on older distros (e.g. Ubuntu 18.04, Debian 9), fall back to fuse
    # Use apt-cache policy to check if fuse3 has an installation candidate (not just metadata)
    if apt-cache policy fuse3 2>/dev/null | grep -q "Candidate:" && ! apt-cache policy fuse3 2>/dev/null | grep -q "Candidate: (none)"; then
        if ! is_pkg_installed "fuse3"; then
            echo "Package fuse3 is missing, will install it."
            MISSING="$MISSING fuse3"
        fi
    else
        echo "fuse3 not available, falling back to fuse"
        if ! is_pkg_installed "fuse"; then
            echo "Package fuse is missing, will install it."
            MISSING="$MISSING fuse"
        fi
    fi

    if [ -n "$MISSING" ]; then
        echo "Missing packages detected, installing:$MISSING"
        DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get -qq -o=Dpkg::Use-Pty=0 install -y --no-install-recommends $MISSING
    else
        echo "All required packages are already installed."
    fi
}

install_packages_rhel() {
    PACKAGES="systemd openssh-server sudo chrony socat curl ca-certificates iptables git nfs-utils less nftables iputils jq passwd"
    # fuse3 is not available on older RHEL/CentOS (e.g. CentOS 7), fall back to fuse
    if command -v dnf >/dev/null 2>&1; then
        if dnf info fuse3 >/dev/null 2>&1; then
            PACKAGES="$PACKAGES fuse3"
        else
            PACKAGES="$PACKAGES fuse"
        fi
    elif command -v yum >/dev/null 2>&1; then
        if yum info fuse3 >/dev/null 2>&1; then
            PACKAGES="$PACKAGES fuse3"
        else
            PACKAGES="$PACKAGES fuse"
        fi
    fi
    echo "Installing packages (yum/dnf): $PACKAGES"
    if command -v dnf >/dev/null 2>&1; then
        dnf install -y --allowerasing $PACKAGES
    elif command -v yum >/dev/null 2>&1; then
        yum install -y $PACKAGES
    fi
}

install_packages_alpine() {
    PACKAGES="openrc openssh-server sudo chrony socat curl ca-certificates iptables git nfs-utils less nftables iputils jq bash shadow"
    # fuse3 may not be available on older Alpine versions
    if apk info -e fuse3 >/dev/null 2>&1 || apk search -x fuse3 2>/dev/null | grep -q fuse3; then
        PACKAGES="$PACKAGES fuse3"
    else
        PACKAGES="$PACKAGES fuse"
    fi
    echo "Installing packages (apk): $PACKAGES"
    apk update
    apk add --no-cache $PACKAGES
}

case "$DISTRO" in
    debian)  install_packages_debian ;;
    rhel)    install_packages_rhel ;;
    alpine)  install_packages_alpine ;;
    *)
        echo "WARNING: Unknown distro, skipping package installation"
        ;;
esac

# Set /dev/fuse permissions to 666 for non-root access
# Use systemd-tmpfiles on systemd distros (Alpine uses local.d, configured below)
if command -v systemctl >/dev/null 2>&1; then
    mkdir -p /etc/tmpfiles.d
    echo 'z /dev/fuse 0666 root root -' > /etc/tmpfiles.d/fuse.conf
fi

echo "Setting up shell"
if [ -x /bin/bash ]; then
    DEFAULT_SHELL="/bin/bash"
else
    DEFAULT_SHELL="/bin/sh"
fi
echo "export SHELL='$DEFAULT_SHELL'" >/etc/profile.d/shell.sh
echo "export PS1='\w \$ '" >/etc/profile.d/prompt.sh
echo "export PS1='\w \$ '" >>"/etc/profile"
mkdir -p /root
touch /root/.bashrc
echo "export PS1='\w \$ '" >>"/root/.bashrc"

echo "Use .bashrc and .profile"
echo "if [ -f ~/.bashrc ]; then . ~/.bashrc; fi; if [ -f ~/.profile ]; then . ~/.profile; fi" >>/etc/profile

echo "Remove root password"
passwd -d root 2>/dev/null || true

echo "Setting up chrony"
# Determine chrony config path based on distro
if [ "$DISTRO" = "rhel" ]; then
    CHRONY_CONF_DIR="/etc"
    CHRONY_CONF="$CHRONY_CONF_DIR/chrony.conf"
else
    CHRONY_CONF_DIR="/etc/chrony"
    CHRONY_CONF="$CHRONY_CONF_DIR/chrony.conf"
fi
mkdir -p "$CHRONY_CONF_DIR"
cat <<EOF >"$CHRONY_CONF"
refclock PHC /dev/ptp0 poll 2 dpoll 2
# Step (jump) the clock instead of slewing when the offset exceeds 1s, but only
# for the first 3 updates after chronyd starts. chronyd restarts on every cold
# boot/reboot, so this corrects a large boot-time offset fast (TLS needs a
# correct clock) without risking a backward jump under a running workload.
# Needed because chrony-wait is masked, so boot no longer blocks on first sync.
makestep 1.0 3
EOF

# Add a proxy config, as some environments expects it there (e.g. timemaster in Node Dockerimage)
if [ "$DISTRO" != "rhel" ]; then
    echo "include /etc/chrony/chrony.conf" >/etc/chrony.conf
fi

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
if command -v systemctl >/dev/null 2>&1; then
    systemctl mask serial-getty@ttyS0.service

    echo "Disable network online wait"
    systemctl mask systemd-networkd-wait-online.service 2>/dev/null || true
    # RHEL/CentOS may use NetworkManager-wait-online instead
    systemctl mask NetworkManager-wait-online.service 2>/dev/null || true

    echo "Disable system first boot wizard"
    # This was problem with Ubuntu 24.04, that differently calculate wizard should be called
    # and Linux boot was stuck in wizard until envd wait timeout
    systemctl mask systemd-firstboot.service 2>/dev/null || true

    echo "Disable chrony-wait"
    # chrony-wait blocks multi-user.target until the first clock sync (~8s);
    # chrony still syncs in the background, nothing needs to wait for it.
    systemctl mask chrony-wait.service 2>/dev/null || true

    echo "Disable slow boot units not needed in the sandbox"
    # binfmt registrations (foreign-arch exec) take ~1s of CPU early in boot and
    # compete with envd start; e2scrub is for LVM-backed ext4 only.
    systemctl mask systemd-binfmt.service 2>/dev/null || true
    systemctl mask e2scrub_reap.service 2>/dev/null || true

    echo "Enable envd service"
    systemctl enable envd.service 2>/dev/null || true
else
    echo "systemctl not found, configuring OpenRC (Alpine)"
    # Create /etc/inittab for busybox init to boot OpenRC
    cat > /etc/inittab <<'INITTAB'
::sysinit:/sbin/openrc sysinit
::sysinit:/sbin/openrc boot
::wait:/sbin/openrc default
ttyS0::respawn:/sbin/getty 38400 ttyS0
::ctrlaltdel:/sbin/reboot
::shutdown:/sbin/openrc shutdown
INITTAB

    # Create /etc/network/interfaces so the networking service doesn't fail.
    # Kernel boot args already configure eth0 via ip=..., so we just need
    # a valid (minimal) interfaces file for ifupdown-ng to parse.
    mkdir -p /etc/network
    cat > /etc/network/interfaces <<'NETIF'
auto lo
iface lo inet loopback

auto eth0
iface eth0 inet manual
NETIF

    # Remove chronyd's hard dependency on networking — it only uses PHC
    # /dev/ptp0 and doesn't need a network stack. Replace "need net" with
    # "use net" (soft dependency) so chronyd starts even if networking fails.
    if [ -f /etc/init.d/chronyd ]; then
        sed -i 's/need net/use net/' /etc/init.d/chronyd
    fi

    # Set /dev/fuse permissions via a local.d script (Alpine has no systemd-tmpfiles)
    mkdir -p /etc/local.d
    cat > /etc/local.d/fuse-permissions.start <<'FUSE'
#!/bin/sh
[ -e /dev/fuse ] && chmod 0666 /dev/fuse
FUSE
    chmod +x /etc/local.d/fuse-permissions.start
    rc-update add local default 2>/dev/null || true

    # Enable envd service via OpenRC
    if [ -f /etc/init.d/envd ]; then
        rc-update add envd default 2>/dev/null || true
    fi
    # Enable chrony and sshd via OpenRC
    rc-update add chronyd default 2>/dev/null || true
    rc-update add sshd default 2>/dev/null || true
    # Ensure OpenRC default runlevel boots properly
    rc-update add devfs sysinit 2>/dev/null || true
    rc-update add dmesg sysinit 2>/dev/null || true
    rc-update add mdev sysinit 2>/dev/null || true
    # hwdrivers needs 'dev' service which mdev provides; only add if mdev is present
    if [ -f /etc/init.d/mdev ]; then
        rc-update add hwdrivers sysinit 2>/dev/null || true
    fi
    rc-update add hostname boot 2>/dev/null || true
    rc-update add bootmisc boot 2>/dev/null || true
    rc-update add sysctl boot 2>/dev/null || true
    rc-update add localmount boot 2>/dev/null || true
    rc-update add networking boot 2>/dev/null || true
    rc-update add syslog boot 2>/dev/null || true
fi

# Clean machine-id from Docker
rm -rf /etc/machine-id

echo "Linking systemd to init"
if [ -f /lib/systemd/systemd ]; then
    ln -sf /lib/systemd/systemd /usr/sbin/init
elif [ -f /usr/lib/systemd/systemd ]; then
    # RHEL/CentOS use /usr/lib/systemd/systemd
    ln -sf /usr/lib/systemd/systemd /usr/sbin/init
else
    echo "systemd not found, skipping init link (Alpine/OpenRC)"
    # envd's process handler uses hardcoded /usr/bin/ionice and /usr/bin/nice paths.
    # On Alpine these are busybox applets at /bin/, so create symlinks.
    [ ! -e /usr/bin/ionice ] && [ -e /bin/ionice ] && ln -sf /bin/ionice /usr/bin/ionice
    [ ! -e /usr/bin/nice ] && [ -e /bin/nice ] && ln -sf /bin/nice /usr/bin/nice
fi

echo "Unlocking immutable configuration"
$BUSYBOX chattr -i /etc/resolv.conf

echo "Finished provisioning script"

# Delete itself
rm -rf /etc/init.d/rcS
rm -rf /usr/local/bin/provision.sh

# Report successful provisioning
printf "0" > "$RESULT_PATH"
