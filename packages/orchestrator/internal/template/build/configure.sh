#!/bin/bash
export BASH_XTRACEFD=1
set -euo pipefail

echo "Starting configuration script"

echo "Enable swap"
echo 0 > /proc/sys/vm/swappiness
swapon /swap/swapfile

# Create default user.
# if the /home/user directory exists, we copy the skeleton files to it because the adduser command
# will ignore the directory if it exists, but we want to include the skeleton files in the home directory
# in our case.
echo "Create default user 'user' (if doesn't exist yet)"
ADDUSER_OUTPUT=$(adduser -disabled-password --gecos "" user 2>&1)
if echo "$ADDUSER_OUTPUT" | grep -q "The home directory \`/home/user' already exists"; then
    # Copy skeleton files if they don't exist in the home directory
    echo "Copy skeleton files to /home/user"
    cp -rn /etc/skel/. /home/user/
fi

echo "Add sudo to 'user' with no password"
usermod -aG sudo user
passwd -d user
echo "user ALL=(ALL:ALL) NOPASSWD: ALL" >>/etc/sudoers

echo "Give 'user' ownership to /home/user"
mkdir -p /home/user
chown -R user:user /home/user

echo "Give 777 permission to /usr/local"
chmod 777 -R /usr/local

echo "Create /code directory"
mkdir -p /code
echo "Give 777 permission to /code"
chmod 777 -R /code

echo "Finished configuration script"
