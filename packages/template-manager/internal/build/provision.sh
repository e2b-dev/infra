export BASH_XTRACEFD=1
set -euo pipefail

echo "Starting provisioning script."

echo "ENV_ID={{ .EnvID }}" >/.e2b
echo "BUILD_ID={{ .BuildID }}" >>/.e2b

# We are downloading the packages manually
install_packages() {
    echo "Installing packages"
    apt-get -qq update --download-only
    DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get -qq install -y openssh-server sudo systemd socat chrony linuxptp iptables > /dev/null
}

install_packages

# Set up autologin.
set_autologin() {
    echo "Configuring autologin"
    mkdir -p /etc/systemd/system/serial-getty@ttyS0.service.d
    cat <<EOF >/etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf
[Service]
ExecStart=
ExecStart=-/sbin/agetty --noissue --autologin root %I 115200,38400,9600 vt102
EOF
}

set_autologin

# Add swapfile — we enable it in the preexec for envd
configure_swap() {
    echo "Configuring swap to ${1} MiB"
    mkdir /swap
    fallocate -l "${1}"M /swap/swapfile
    chmod 600 /swap/swapfile
    mkswap /swap/swapfile
}

configure_swap 128

# Set up envd v0.0.1 service.
setup_service_envd_v0_0_1() {
    mkdir -p /etc/systemd/system
    cat <<EOF >/etc/systemd/system/envd-v0.0.1.service
[Unit]
Description=Env v0.0.1 Daemon Service

[Service]
Type=simple
Restart=always
User=root
Group=root
Environment=GOTRACEBACK=all
LimitCORE=infinity
ExecStart=/bin/bash -l -c "/usr/bin/envd-v0.0.1"
OOMPolicy=continue
OOMScoreAdjust=-1000
Environment="GOMEMLIMIT={{ .MemoryLimit }}MiB"

[Install]
WantedBy=multi-user.target
EOF
}

setup_service_envd_v0_0_1

# Set up envd service.
setup_service_envd() {
    echo "Setting up envd service"
    cat <<EOF >/etc/systemd/system/envd.service
[Unit]
Description=Env Daemon Service

[Service]
Type=simple
Restart=always
User=root
Group=root
Environment=GOTRACEBACK=all
LimitCORE=infinity
ExecStart=/bin/bash -l -c "/usr/bin/envd -cmd '{{ .StartCmd }}'"
OOMPolicy=continue
OOMScoreAdjust=-1000
Environment="GOMEMLIMIT={{ .MemoryLimit }}MiB"

ExecStartPre=/bin/bash -c 'echo 0 > /proc/sys/vm/swappiness && swapon /swap/swapfile'

[Install]
WantedBy=multi-user.target
EOF
}

setup_service_envd

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

# Enable systemd services
# Because this script runs in a container we can't use `systemctl`.
# Containers don't run init daemons. We have to enable the runner service manually.
mkdir -p /etc/systemd/system/multi-user.target.wants
ln -s /etc/systemd/system/envd.service /etc/systemd/system/multi-user.target.wants/envd.service
ln -s /etc/systemd/system/envd-v0.0.1.service /etc/systemd/system/multi-user.target.wants/envd-v0.0.1.service

# Set up shell.
echo "Setting up shell"
echo "export SHELL='/bin/bash'" >/etc/profile.d/shell.sh
echo "export PS1='\w \$ '" >/etc/profile.d/prompt.sh
echo "export PS1='\w \$ '" >>"/etc/profile"
echo "export PS1='\w \$ '" >>"/root/.bashrc"

# Use .bashrc and .profile
echo "Use .bashrc and .profile"
echo "if [ -f ~/.bashrc ]; then source ~/.bashrc; fi; if [ -f ~/.profile ]; then source ~/.profile; fi" >>/etc/profile

# Set up SSH.
setup_ssh() {
    echo "Setting up SSH"
    mkdir -p /etc/ssh
    cat <<EOF >>/etc/ssh/sshd_config
PermitRootLogin yes
PermitEmptyPasswords yes
PasswordAuthentication yes
EOF
}

setup_ssh

# Remove password for root.
echo "Remove root password"
passwd -d root

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

echo "Create /code directory"
mkdir -p /code
mkdir -p /home/user

echo "Give 'user' 777 permission to /code and /usr/local"
chown -R user:user /home/user
chmod 777 -R /usr/local
chmod 777 -R /code

# Add DNS.
echo "Configure DNS"
echo "nameserver 8.8.8.8" >/etc/resolv.conf

# Start systemd services
systemctl enable envd
systemctl enable envd-v0.0.1
systemctl enable chrony 2>&1

echo "Forwarding ports"
cat <<EOF >/etc/systemd/system/forward_ports.service
[Unit]
Description=Forward Ports Service

[Service]
Type=simple
Restart=no
User=root
Group=root
ExecStart=/bin/bash -l -c "(echo 1 | tee /proc/sys/net/ipv4/ip_forward) && iptables-legacy -t nat -A POSTROUTING -s 127.0.0.1 -j SNAT --to-source {{ .FcAddress }} && iptables-legacy -t nat -A PREROUTING -d {{ .FcAddress }} -j DNAT --to-destination 127.0.0.1"

[Install]
WantedBy=multi-user.target
EOF

# systemctl enable forward_ports

echo "Finished provisioning script"
