echo "Don't wait for ttyS0 (serial console kernel logs)"
# This is required when the Firecracker kernel args has specified console=ttyS0
systemctl mask serial-getty@ttyS0.service

echo "Disable network online wait"
systemctl mask systemd-networkd-wait-online.service

echo "Disable system first boot wizard"
# This was problem with Ubuntu 24.04, that differently calculate wizard should be called
# and Linux boot was stuck in wizard until envd wait timeout
systemctl mask systemd-firstboot.service

echo "Enable time synchronization ($E2B_TIMESYNC_UNIT)"
# Distro-correct chrony unit (chrony on Debian, chronyd on RHEL/Arch).
systemctl enable "$E2B_TIMESYNC_UNIT"

echo "Pull the boot-time time-source selector into $E2B_TIMESYNC_UNIT"
# e2b-chrony-source.service writes the source line chrony.conf includes; the
# unit that must pull it in is only known here (the name differs per family).
mkdir -p "/etc/systemd/system/$E2B_TIMESYNC_UNIT.service.d"
printf '[Unit]\nRequires=e2b-chrony-source.service\nAfter=e2b-chrony-source.service\n' \
    >"/etc/systemd/system/$E2B_TIMESYNC_UNIT.service.d/e2b-chrony-source.conf"

echo "Enable SSH ($E2B_SSH_UNIT)"
# provision.sh writes the sandbox sshd_config on every family, but nothing was
# turning the unit on: Debian's postinst and the RHEL RPM scriptlet enable it
# themselves, Arch does not, so Arch sandboxes shipped with SSH configured and
# dead. Enabling is idempotent where the packaging already did it.
systemctl enable "$E2B_SSH_UNIT.service"

echo "Enable envd autostart"
# Belt-and-suspenders with the baked 00-e2b.preset: on the RHEL family the
# package transaction above runs systemd's RPM scriptlet 'systemctl preset-all'
# (policy 'disable *'), which deletes the baked wants-symlink.
systemctl enable envd.service

echo "Disable chrony-wait"
# chrony-wait blocks multi-user.target until the first clock sync (~8s);
# chrony still syncs in the background, nothing needs to wait for it.
# masking a unit that doesn't exist on this distro still succeeds (systemctl
# mask just writes the /dev/null symlink), so a failure here is real.
systemctl mask chrony-wait.service

echo "Disable slow boot units not needed in the sandbox"
# binfmt registrations (foreign-arch exec) take ~1s of CPU early in boot and
# compete with envd start; e2scrub is for LVM-backed ext4 only.
systemctl mask systemd-binfmt.service
systemctl mask e2scrub_reap.service
