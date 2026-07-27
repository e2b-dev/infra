#!/bin/bash
export BASH_XTRACEFD=1
set -euo pipefail

echo "Starting configuration script"

cat <<EOF > /.e2b
ENV_ID={{ .TemplateID }}
TEMPLATE_ID={{ .TemplateID }}
BUILD_ID={{ .BuildID }}
EOF

# Create default user. useradd is in shadow(-utils) on every supported distro
# family; -m creates the home dir, -s the shell. A creation failure is a real
# error — a template whose default user silently doesn't exist fails much more
# confusingly later.
echo "Create default user 'user' (if doesn't exist yet)"
if ! id -u user >/dev/null 2>&1; then
    useradd -m -s /bin/bash user
fi
# useradd -m skips skeleton files when /home/user already exists, so copy them
# explicitly (no-clobber). Not every image ships /etc/skel — say so instead of
# hiding it. Walked file-by-file because `cp -n` exits non-zero when it skips an
# existing file on coreutils >= 9.2 (Fedora 40+), which is a skip, not a failure.
if [ -d /home/user ]; then
    if [ -d /etc/skel ]; then
        echo "Copy skeleton files to /home/user (keeping existing files)"
        (cd /etc/skel && find . -type f -print | while IFS= read -r f; do
            if [ ! -e "/home/user/$f" ]; then
                mkdir -p "/home/user/$(dirname "$f")"
                cp -a "/etc/skel/$f" "/home/user/$f"
            fi
        done)
    else
        echo "No /etc/skel on this image; skipping skeleton copy"
    fi
fi

echo "Add sudo to 'user' with no password"
# Admin group differs by distro (sudo on Debian/Ubuntu, wheel elsewhere); the
# NOPASSWD sudoers entry below is what actually grants privileges. Neither
# group existing is a real error.
if getent group sudo >/dev/null; then
    usermod -aG sudo user
elif getent group wheel >/dev/null; then
    usermod -aG wheel user
else
    echo "ERROR: neither the sudo nor the wheel group exists on this image" >&2
    exit 1
fi
passwd -d user
# Skip the append when the entry is already present.
if grep -q '^user ALL=(ALL:ALL) NOPASSWD: ALL' /etc/sudoers; then
    echo "sudoers entry already present"
else
    echo "user ALL=(ALL:ALL) NOPASSWD: ALL" >>/etc/sudoers
fi

echo "Give 'user' ownership to /home/user"
mkdir -p /home/user
chown -R user:user /home/user

echo "Give 777 permission to /usr/local"
chmod -R 777 /usr/local

echo "Create /code directory"
mkdir -p /code
echo "Give 777 permission to /code"
chmod -R 777 /code

echo "Finished configuration script"
