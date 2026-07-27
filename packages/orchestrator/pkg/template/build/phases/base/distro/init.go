// Init-system axis of the distro profiles: one provisioning-time shell block
// per init system, rendered into the selector as e2b_init_setup() so provision.sh
// stays init-agnostic.
package distro

import "strings"

// InitSystem is the guest init family a profile boots with.
type InitSystem string

const (
	// InitSystemd — Debian/Ubuntu, RHEL/Fedora, Arch (systemd via envd.service).
	InitSystemd InitSystem = "systemd"
	// InitOpenRC — Alpine (busybox init → OpenRC via the baked /etc/init.d/envd).
	InitOpenRC InitSystem = "openrc"
)

// initSetup is the provisioning-time shell block per init system. Bodies may
// reference the selector's profile variables (e.g. $E2B_TIMESYNC_UNIT), defined
// in the same case arm before the function is called.
var initSetup = map[InitSystem]string{
	InitSystemd: `echo "Don't wait for ttyS0 (serial console kernel logs)"
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
systemctl mask e2scrub_reap.service`,

	// OpenRC (Alpine). The image is still running the one-shot PROVISIONING
	// inittab (it is what launched this script); replace it with the real
	// boot sequence and wire the runlevels a container image ships without.
	InitOpenRC: `echo "Installing boot inittab (busybox init -> OpenRC runlevels)"
printf '%s\n' \
    '::sysinit:/sbin/openrc sysinit' \
    '::sysinit:/sbin/openrc boot' \
    '::wait:/sbin/openrc default' \
    '::shutdown:/sbin/openrc shutdown' \
    '::ctrlaltdel:/sbin/reboot' \
    > /etc/inittab

echo "Registering base OpenRC services"
# Container images carry no runlevel wiring at all (setup-alpine does this on
# real installs): kernel filesystems in sysinit, system prep in boot. bootmisc
# also wipes /tmp in the boot runlevel, so envd (default runlevel) can never
# race the wipe — the ordering systemd needs After= for is inherent here.
# Which scripts exist varies by image (mdev vs udev, procfs presence) — check
# and say so instead of swallowing rc-update errors.
for svc in devfs sysfs procfs dmesg mdev; do
    if [ -e "/etc/init.d/$svc" ]; then
        rc-update add "$svc" sysinit
    else
        echo "OpenRC service $svc not present on this image; skipping"
    fi
done
for svc in localmount sysctl hostname bootmisc; do
    if [ -e "/etc/init.d/$svc" ]; then
        rc-update add "$svc" boot
    else
        echo "OpenRC service $svc not present on this image; skipping"
    fi
done

# The FC guest's eth0 is configured by the kernel (ip=), but OpenRC services
# declaring a "need net" dependency (chronyd) trigger the networking service,
# which errors out on a missing /etc/network/interfaces and takes chronyd
# down with it. A loopback-only interfaces file lets networking start (and
# provide "net") without touching the kernel-managed eth0.
printf 'auto lo\niface lo inet loopback\n' > /etc/network/interfaces
if [ -e /etc/init.d/networking ]; then
    rc-update add networking boot
else
    echo "OpenRC networking service not present on this image; services needing 'net' must not be enabled"
fi

echo "Enable time synchronization ($E2B_TIMESYNC_UNIT)"
rc-update add "$E2B_TIMESYNC_UNIT" default

echo "Enable envd autostart"
# The service script is baked at a neutral path (envd.openrc.tpl) so the
# Debian family's update-rc.d never sees it; install it for OpenRC here.
cp /usr/local/share/e2b/envd.openrc /etc/init.d/envd
chmod 0755 /etc/init.d/envd
rc-update add envd default

echo "Enable sshd"
if [ -e /etc/init.d/sshd ]; then
    rc-update add sshd default
else
    echo "sshd service not present on this image; skipping"
fi`,
}

// indentBlock indents every non-empty line of a shell block for embedding
// inside the generated selector function bodies.
func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}

	return strings.Join(lines, "\n")
}
