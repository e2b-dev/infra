#!/bin/sh
set -eu

echo "Starting configuration script"

cat <<EOF > /.e2b
ENV_ID={{ .TemplateID }}
TEMPLATE_ID={{ .TemplateID }}
BUILD_ID={{ .BuildID }}
EOF

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

# Create default user.
# if the /home/user directory exists, we copy the skeleton files to it because the adduser command
# will ignore the directory if it exists, but we want to include the skeleton files in the home directory
# in our case.
echo "Create default user 'user' (if doesn't exist yet)"

create_user_debian() {
    ADDUSER_OUTPUT=$(adduser --disabled-password --gecos "" user 2>&1 || true)
    echo "$ADDUSER_OUTPUT"
    if echo "$ADDUSER_OUTPUT" | grep -q "The home directory.*already exists"; then
        echo "Copy skeleton files to /home/user"
        cp -rn /etc/skel/. /home/user/
    fi
    echo "Add sudo to 'user' with no password"
    usermod -aG sudo user
}

create_user_rhel() {
    if ! id user >/dev/null 2>&1; then
        useradd -m user 2>&1 || true
    else
        echo "User 'user' already exists"
        # Copy skeleton files if home exists
        if [ -d /home/user ]; then
            cp -rn /etc/skel/. /home/user/ 2>/dev/null || true
        fi
    fi
    echo "Add sudo to 'user' with no password (wheel group)"
    usermod -aG wheel user 2>/dev/null || true
    # Also try sudo group in case it exists
    usermod -aG sudo user 2>/dev/null || true
}

create_user_alpine() {
    if ! id user >/dev/null 2>&1; then
        adduser -D -s /bin/sh user 2>&1 || true
    else
        echo "User 'user' already exists"
    fi
    echo "Add sudo to 'user' with no password (wheel group)"
    addgroup user wheel 2>/dev/null || true
}

case "$DISTRO" in
    debian)  create_user_debian ;;
    rhel)    create_user_rhel ;;
    alpine)  create_user_alpine ;;
    *)
        # Fallback: try useradd first, then adduser
        if command -v useradd >/dev/null 2>&1; then
            useradd -m user 2>/dev/null || true
            usermod -aG wheel user 2>/dev/null || usermod -aG sudo user 2>/dev/null || true
        elif command -v adduser >/dev/null 2>&1; then
            adduser -D user 2>/dev/null || adduser --disabled-password --gecos "" user 2>/dev/null || true
        fi
        ;;
esac

passwd -d user 2>/dev/null || true
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
