{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/init.d/envd" 0o755 }}

#!/sbin/openrc-run
# E2B env daemon — the OpenRC counterpart of envd.service (see envd.service.tpl
# for the full rationale on each step; this mirrors it for the Alpine/OpenRC
# family). Baked into every image; on systemd distros it is inert — systemd's
# sysv-generator skips /etc/init.d scripts shadowed by a native unit, and the
# native envd.service exists there.
#
# /tmp-wipe ordering note: OpenRC's bootmisc (boot runlevel) wipes /tmp before
# the default runlevel starts, so envd — in default — can never answer an
# update-envd upload before the wipe. The race envd.service needs an explicit
# After=systemd-tmpfiles-setup.service for cannot happen here.

description="E2B env daemon"

supervisor=supervise-daemon
command=/usr/bin/envd
supervise_daemon_args="--env GOTRACEBACK=all --env GOMEMLIMIT={{ .MemoryLimit }}MiB --stdout /var/log/envd.log --stderr /var/log/envd.log"
# Retry forever (envd.service uses Restart=always + StartLimitIntervalSec=0).
respawn_delay=1
respawn_max=0

depend() {
    need localmount
    after bootmisc
    use net
}

start_pre() {
    # Seed a tmpfs-backed /etc/ssl/certs exactly like envd.service's
    # ExecStartPre: prefer the ssl-certs.tar packed as the build's last guest
    # step, fall back to copying the current cert dir, and never fail the
    # service over a missing regeneration tool.
    if ! mountpoint -q /etc/ssl/certs; then
        mkdir -p /run/e2b/certs
        tar -C /run/e2b/certs -xf /usr/local/share/e2b/ssl-certs.tar 2>/dev/null \
            || cp -a /etc/ssl/certs/. /run/e2b/certs/ 2>/dev/null
        mount -o bind /run/e2b/certs /etc/ssl/certs
    fi
    [ -s /etc/ssl/certs/ca-certificates.crt ] \
        || ! command -v update-ca-certificates >/dev/null 2>&1 \
        || update-ca-certificates
    # systemd-tmpfiles applies the fuse.conf tmpfiles.d rule on the systemd
    # family; OpenRC has no tmpfiles pass, so set the mode here.
    chmod 666 /dev/fuse 2>/dev/null || true
    return 0
}
