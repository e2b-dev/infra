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
ADDUSER_OUTPUT=$($bb adduser -D -g "" user 2>&1 || true)
echo "$ADDUSER_OUTPUT"
if $bb echo "$ADDUSER_OUTPUT" | $bb grep -q "The home directory \`/home/user' already exists"; then
    echo "Copy skeleton files to /home/user"
    for f in /etc/skel/.[!.]* /etc/skel/*; do
        $bb [ -e "$f" ] || continue
        dest="/home/user/$($bb basename "$f")"
        $bb [ -e "$dest" ] || $bb cp -r "$f" "$dest"
    done
fi

echo "Add sudo to 'user' with no password"
$bb addgroup user sudo
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
