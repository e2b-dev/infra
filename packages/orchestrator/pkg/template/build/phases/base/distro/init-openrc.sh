# OpenRC (Alpine). The image is still running the one-shot PROVISIONING
# inittab (it is what launched this script); replace it with the real
# boot sequence and wire the runlevels a container image ships without.
echo "Installing boot inittab (busybox init -> OpenRC runlevels)"
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
    # openrc ships this script and the profile installs openrc, so this is
    # unreachable on the images we support. If it ever fires, nothing provides
    # "net": chronyd is still enabled below so the failure shows up in the boot
    # log rather than the sandbox silently running without time sync.
    echo "OpenRC networking service not present on this image; nothing provides 'net', so time sync will fail to start"
fi

echo "Enable time synchronization ($E2B_TIMESYNC_UNIT)"
rc-update add "$E2B_TIMESYNC_UNIT" default

echo "Install the boot-time time-source selector"
# Writes the source line chrony.conf includes, in the boot runlevel so it is
# done before chronyd starts in default. Baked outside /etc/init.d for the same
# reason as the envd service script.
cp /usr/local/share/e2b/chrony-source.openrc /etc/init.d/e2b-chrony-source
chmod 0755 /etc/init.d/e2b-chrony-source
rc-update add e2b-chrony-source boot

echo "Disabling the chronyd seccomp filter"
# Alpine's OpenRC init script hardcodes '-F 1', which loads chronyd's seccomp
# filter, and Alpine's chrony build (-NTS -SECHASH -DEBUG) takes a SIGSYS — "Bad
# system call" right after "Loaded seccomp filter (level 1)" — as soon as the PHC
# refclock is driven, leaving the service in OpenRC's "crashed" state with the
# clock unsynced. Unconditional, NOT gated on the PHC being present: which source
# chronyd drives is decided at boot by e2b-chrony-source, so provisioning cannot
# know. It costs nothing when the pool branch is taken, and the systemd families
# pass no -F at all, so this is parity rather than a new hole. The init script
# splices $command_args in after its own -F 1 and chronyd honours the last -F.
# conf.d must be named for the init script OpenRC sources it for.
mkdir -p /etc/conf.d
echo 'command_args="-F 0"' >>"/etc/conf.d/$E2B_TIMESYNC_UNIT"

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
fi
