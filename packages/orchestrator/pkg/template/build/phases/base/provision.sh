#!/bin/sh
set -eu

BUSYBOX="{{ .BusyBox }}"
RESULT_PATH="{{ .ResultPath }}"

echo "Starting provisioning script"

{{ if eq .Provider "gcp" }}
# GCP Specific logic
{{ end }}

# Helper function to check if a package is installed
is_package_installed() {
    dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed"
}

# Helper: idempotently append a line to a file only if the exact line isn't present.
# Usage: append_line_if_missing <line> <file>
append_line_if_missing() {
    line=$1
    file=$2
    grep -qxF "$line" "$file" 2>/dev/null || echo "$line" >>"$file"
}

# Helper: check whether a file has the immutable (`i`) attribute set.
# Returns 0 if immutable, non-zero otherwise.
# Usage: is_immutable <file>
is_immutable() {
    $BUSYBOX lsattr "$1" 2>/dev/null | cut -c5 | grep -q i
}

echo "Making configuration immutable"
# Idempotent: only set immutable bit if not already set
if ! is_immutable /etc/resolv.conf; then
    $BUSYBOX chattr +i /etc/resolv.conf
fi

# Install required packages if not already installed
PACKAGES="systemd systemd-sysv openssh-server sudo chrony socat curl ca-certificates fuse3 iptables git nfs-common"
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
    apt-get -q update
    DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get -qq -o=Dpkg::Use-Pty=0 install -y --no-install-recommends $MISSING
else
    echo "All required packages are already installed."
fi

# Set /dev/fuse permissions to 666 for non-root access
# Use systemd-tmpfiles to set permissions at boot
mkdir -p /etc/tmpfiles.d
echo 'z /dev/fuse 0666 root root -' > /etc/tmpfiles.d/fuse.conf

echo "Setting up shell"
# These two are idempotent (`>` truncates)
echo "export SHELL='/bin/bash'" >/etc/profile.d/shell.sh
echo "export PS1='\w \$ '" >/etc/profile.d/prompt.sh

# Idempotent: only append PS1 if the exact line isn't present yet
PS1_LINE="export PS1='\w \$ '"
append_line_if_missing "$PS1_LINE" /etc/profile
append_line_if_missing "$PS1_LINE" /root/.bashrc

echo "Use .bashrc and .profile"
# Idempotent: only append the source snippet if not already present
SOURCE_LINE='if [ -f ~/.bashrc ]; then source ~/.bashrc; fi; if [ -f ~/.profile ]; then source ~/.profile; fi'
append_line_if_missing "$SOURCE_LINE" /etc/profile

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
touch /etc/ssh/sshd_config
# Idempotent: only append each directive if not already present
for ssh_line in "PermitRootLogin yes" "PermitEmptyPasswords yes" "PasswordAuthentication yes"; do
    append_line_if_missing "$ssh_line" /etc/ssh/sshd_config
done

echo "Increasing inotify watch limit"
# Idempotent: only append if not already present
append_line_if_missing 'fs.inotify.max_user_watches=65536' /etc/sysctl.conf

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

# Clean machine-id from Docker
rm -rf /etc/machine-id

echo "Linking systemd to init"
ln -sf /lib/systemd/systemd /usr/sbin/init

echo "Unlocking immutable configuration"
# Idempotent: only unset immutable bit if currently set
if is_immutable /etc/resolv.conf; then
    $BUSYBOX chattr -i /etc/resolv.conf
fi

echo "Finished provisioning script"

# Delete itself (idempotent with -f: no error if already removed)
rm -f /etc/init.d/rcS
rm -f /usr/local/bin/provision.sh

# Report successful provisioning
printf "0" > "$RESULT_PATH"
