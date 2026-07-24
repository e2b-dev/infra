#!/bin/bash
export BASH_XTRACEFD=1
set -euo pipefail

echo "Starting configuration script"

cat <<EOF > /.e2b
ENV_ID={{ .TemplateID }}
TEMPLATE_ID={{ .TemplateID }}
BUILD_ID={{ .BuildID }}
EOF

# Create default user. useradd is part of shadow(-utils) and present on every
# supported distro family (Debian/Ubuntu, RHEL/Fedora, Arch), unlike Debian's
# adduser wrapper (FEAT-145). -m creates the home dir; -s sets the shell.
echo "Create default user 'user' (if doesn't exist yet)"
if ! id -u user >/dev/null 2>&1; then
    useradd -m -s /bin/bash user || true
fi
# useradd -m skips skeleton files when /home/user already exists, so copy them
# explicitly (no-clobber) to match the previous adduser behaviour.
if [ -d /home/user ]; then
    echo "Copy skeleton files to /home/user"
    cp -rn /etc/skel/. /home/user/ 2>/dev/null || true
fi

echo "Add sudo to 'user' with no password"
# Admin group differs by distro (sudo on Debian/Ubuntu, wheel elsewhere); the
# NOPASSWD sudoers entry below is what actually grants privileges.
usermod -aG sudo user 2>/dev/null || usermod -aG wheel user 2>/dev/null || true
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
