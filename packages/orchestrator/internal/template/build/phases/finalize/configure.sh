#!/bin/bash
export BASH_XTRACEFD=1
set -euo pipefail
bb={{ .BusyBox }}

echo "Starting configuration script"

$bb cat <<EOF > /.e2b
ENV_ID={{ .TemplateID }}
TEMPLATE_ID={{ .TemplateID }}
BUILD_ID={{ .BuildID }}
EOF

# Create default user.
# if the /home/user directory exists, we copy the skeleton files to it because the adduser command
# will ignore the directory if it exists, but we want to include the skeleton files in the home directory
# in our case.
echo "Create default user 'user' (if doesn't exist yet)"
HOME_EXISTED=false
if $bb [ -d /home/user ]; then
    HOME_EXISTED=true
fi
USER_CREATED=false
ADDUSER_OUTPUT=$($bb adduser -D -g "" user 2>&1) && USER_CREATED=true || true
echo "$ADDUSER_OUTPUT"
if $bb [ "$HOME_EXISTED" = "true" ] && $bb [ "$USER_CREATED" = "true" ]; then
    echo "Copy skeleton files to /home/user"
    # Use find to walk all files/symlinks in /etc/skel and copy each one
    # only if the destination doesn't exist — this merges into existing
    # directories (unlike a top-level check that would skip entire dirs).
    $bb find /etc/skel -mindepth 1 | while read -r src; do
        dest="/home/user/${src#/etc/skel/}"
        if $bb [ -d "$src" ]; then
            $bb mkdir -p "$dest"
        elif ! $bb [ -e "$dest" ]; then
            $bb mkdir -p "$($bb dirname "$dest")"
            $bb cp "$src" "$dest"
        fi
    done
fi

echo "Add sudo to 'user' with no password"
$bb addgroup user sudo || true
$bb passwd -d user
echo "user ALL=(ALL:ALL) NOPASSWD: ALL" >>/etc/sudoers

echo "Give 'user' ownership to /home/user"
$bb mkdir -p /home/user
$bb chown -R user:user /home/user

echo "Give 777 permission to /usr/local"
$bb chmod 777 -R /usr/local

echo "Create /code directory"
$bb mkdir -p /code
echo "Give 777 permission to /code"
$bb chmod 777 -R /code

echo "Finished configuration script"
