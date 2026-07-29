{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/usr/local/share/e2b/envd.openrc" 0o755 }}

#!/sbin/openrc-run
# E2B env daemon — the OpenRC counterpart of envd.service (see envd.service.tpl
# for the full rationale on each step; this mirrors it for the Alpine/OpenRC
# family). Baked at a neutral path and installed to /etc/init.d/envd by the
# OpenRC e2b_init_setup only: if it lived in /etc/init.d on every image,
# Debian's `systemctl enable envd` would hand it to update-rc.d, which aborts
# on a non-LSB script and fails the whole provisioning.
#
# /tmp-wipe ordering note: OpenRC's bootmisc (boot runlevel) wipes /tmp before
# the default runlevel starts, so envd — in default — can never answer an
# update-envd upload before the wipe. The race envd.service needs an explicit
# After=systemd-tmpfiles-setup.service for cannot happen here.

description="E2B env daemon"

supervisor=supervise-daemon
command=/usr/bin/envd
# --nicelevel/--ionice/--oom-score-adj mirror envd.service's Nice=-20,
# IOSchedulingClass=realtime + IOSchedulingPriority=4 (ionice class 1, data 4)
# and OOMScoreAdjust=-1000 — without them envd runs at default priority and is
# the first OOM-kill candidate on Alpine. (The unit's cgroup weights —
# MemoryMin/CPUWeight/IOWeight — have no supervise-daemon equivalent.)
supervise_daemon_args="--nicelevel -20 --ionice 1:4 --oom-score-adj -1000 --env GOTRACEBACK=all --env GOMEMLIMIT={{ .MemoryLimit }}MiB --stdout /var/log/envd.log --stderr /var/log/envd.log"
# Retry forever (envd.service uses Restart=always + StartLimitIntervalSec=0).
respawn_delay=1
respawn_max=0

depend() {
    need localmount
    after bootmisc
    use net
}

start_pre() {
    # Shared with envd.service's ExecStartPre; warns-and-continues by design.
    /usr/local/bin/e2b-seed-certs
    # systemd-tmpfiles applies the fuse.conf tmpfiles.d rule on the systemd
    # family; OpenRC has no tmpfiles pass, so set the mode here.
    if [ -e /dev/fuse ]; then
        chmod 666 /dev/fuse
    fi
    return 0
}
