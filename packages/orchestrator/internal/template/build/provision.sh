#!/bin/bash
set -euo pipefail

echo "Starting provisioning script"

# Set up shell.
echo "Setting up shell"
echo "export SHELL='/bin/bash'" >/etc/profile.d/shell.sh
echo "export PS1='\w \$ '" >/etc/profile.d/prompt.sh
echo "export PS1='\w \$ '" >>"/etc/profile"
echo "export PS1='\w \$ '" >>"/root/.bashrc"

# Use .bashrc and .profile
echo "Use .bashrc and .profile"
echo "if [ -f ~/.bashrc ]; then source ~/.bashrc; fi; if [ -f ~/.profile ]; then source ~/.profile; fi" >>/etc/profile

# Remove password for root.
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

echo "Finished provisioning script"