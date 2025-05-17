#!/bin/bash
export BASH_XTRACEFD=1
set -euo pipefail

echo "Starting provisioning script"

# Install required packages if not already installed
PACKAGES="openssh-server sudo socat chrony linuxptp"
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
    DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get install -y --no-install-recommends "${MISSING[@]}"
else
    echo "All required packages are already installed."
fi

# Add swapfile — we enable it in the preexec for envd
configure_swap() {
    echo "Configuring swap to ${1} MiB"
    mkdir /swap
    fallocate -l "${1}"M /swap/swapfile
    chmod 600 /swap/swapfile
    mkswap /swap/swapfile
    # Enable swap immediately
    echo 0 > /proc/sys/vm/swappiness
    swapon /swap/swapfile
}

configure_swap 128

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

# Start systemd services
systemctl daemon-reload
systemctl enable chrony 2>&1

echo "Finished provisioning script"

exit 0