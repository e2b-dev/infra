#!/bin/bash
set -euo pipefail

echo "Starting provisioning script"

echo "Mounting basic filesystems"
mount -t proc none /proc
mount -t sysfs none /sys
mount -t devtmpfs none /dev

echo "Updating apt"
apt update

echo "Installing packages"
DEBIAN_FRONTEND=noninteractive apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends --no-upgrade systemd systemd-sysv
apt-get clean
rm -rf \
	/var/lib/apt/lists/* \
	/etc/machine-id


echo "Setting up shell"
echo "export SHELL='/bin/bash'" >/etc/profile.d/shell.sh
echo "export PS1='\w \$ '" >/etc/profile.d/prompt.sh
echo "export PS1='\w \$ '" >>"/etc/profile"
echo "export PS1='\w \$ '" >>"/root/.bashrc"

echo "Use .bashrc and .profile"
echo "if [ -f ~/.bashrc ]; then source ~/.bashrc; fi; if [ -f ~/.profile ]; then source ~/.profile; fi" >>/etc/profile

echo "Remove root password"
passwd -d root

echo "Setting up SSH"
mkdir -p /etc/ssh
cat <<EOF >>/etc/ssh/sshd_config
PermitRootLogin yes
PermitEmptyPasswords yes
PasswordAuthentication yes
EOF

echo "Don't wait for ttyS0 (serial console kernel logs)"
systemctl mask serial-getty@ttyS0.service

echo "Linking systemd to init"
ln -sf /lib/systemd/systemd /usr/sbin/init
ln -sf /lib/systemd/systemd /sbin/init

echo "Finished provisioning script"

poweroff -f
echo "Shutdown"