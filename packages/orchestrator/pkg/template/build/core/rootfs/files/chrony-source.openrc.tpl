{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "usr/local/share/e2b/chrony-source.openrc" 0o755 }}

#!/sbin/openrc-run
# OpenRC counterpart of e2b-chrony-source.service (Alpine): runs the shared
# selector in the boot runlevel, before chronyd starts in default. Baked at a
# neutral path and installed into /etc/init.d by the OpenRC e2b_init_setup only
# — on a systemd image the sysv generator would turn an /etc/init.d script of
# this name into a unit that shadows the real one (and Debian's update-rc.d
# aborts on non-LSB scripts, as envd.openrc.tpl explains).

description="E2B: select the chrony time source (PHC refclock if /dev/ptp0, else the NTP pool)"

depend() {
    before chronyd
}

start() {
    ebegin "Selecting the chrony time source"
    /usr/local/bin/e2b-chrony-source
    eend $?
}

stop() {
    # Nothing to undo — the source file lives on tmpfs.
    return 0
}
