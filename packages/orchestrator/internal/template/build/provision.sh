#!/bin/bash
set -euo pipefail

echo "Starting provisioning script"

# fix: dpkg-statoverride: warning: --update given but /var/log/chrony does not exist
mkdir -p /var/log/chrony

echo "Making configuration immutable"
chattr +i /etc/resolv.conf

# Install required packages if not already installed
PACKAGES="systemd systemd-sysv openssh-server sudo chrony linuxptp socat"
echo "Checking presence of the following packages: $PACKAGES"

MISSING=()
for pkg in $PACKAGES; do
    if ! dpkg-query -W -f='${Status}' "$pkg" 2>/dev/null | grep -q "install ok installed"; then
        echo "Package $pkg is missing, will install it."
        MISSING+=("$pkg")
    fi
done

if [ ${#MISSING[@]} -ne 0 ]; then
    echo "Missing packages detected, installing: ${MISSING[*]}"
    apt-get -qq update
    DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get -qq -o=Dpkg::Use-Pty=0 install -y --no-install-recommends "${MISSING[@]}"
else
    echo "All required packages are already installed."
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

# Set up chrony.
setup_chrony(){
    echo "Setting up chrony"
    mkdir -p /etc/chrony
    cat <<EOF >/etc/chrony/chrony.conf
refclock PHC /dev/ptp0 poll -1 dpoll -1 offset 0 trust prefer
makestep 1 -1
EOF

    # Add a proxy config, as some environments expects it there (e.g. timemaster in Node Dockerimage)
    echo "include /etc/chrony/chrony.conf" >/etc/chrony.conf

    mkdir -p /etc/systemd/system/chrony.service.d
    # The ExecStart= should be emptying the ExecStart= line in config.
    cat <<EOF >/etc/systemd/system/chrony.service.d/override.conf
[Service]
ExecStart=
ExecStart=/usr/sbin/chronyd
User=root
Group=root
EOF
}

setup_chrony

echo "Setting up SSH"
mkdir -p /etc/ssh
cat <<EOF >>/etc/ssh/sshd_config
PermitRootLogin yes
PermitEmptyPasswords yes
PasswordAuthentication yes
EOF

configure_swap() {
    echo "Configuring swap to ${1} MiB"
    mkdir /swap
    fallocate -l "${1}"M /swap/swapfile
    chmod 600 /swap/swapfile
    mkswap /swap/swapfile
}

configure_swap 128

echo "Don't wait for ttyS0 (serial console kernel logs)"
# This is required when the Firecracker kernel args has specified console=ttyS0
systemctl mask serial-getty@ttyS0.service

echo "Disable network online wait"
systemctl mask systemd-networkd-wait-online.service

# Clean machine-id from Docker
rm -rf /etc/machine-id

echo "Linking systemd to init"
ln -sf /lib/systemd/systemd /usr/sbin/init

echo "Unlocking immutable configuration"
chattr -i /etc/resolv.conf

echo "Finished provisioning script"

# Delete itself
rm -rf /etc/init.d/rcS
rm -rf /usr/local/bin/provision.sh

# Report successful provisioning
echo -n "0" > "{{ .ResultPath }}"
