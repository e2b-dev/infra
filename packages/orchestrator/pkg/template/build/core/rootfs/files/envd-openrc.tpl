{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/init.d/envd" 0o755 }}
#!/sbin/openrc-run

description="Env Daemon Service"

command="/usr/bin/envd"
command_background=true
pidfile="/run/envd.pid"

# Start as early as possible
start_stop_daemon_args="--nicelevel -20"

depend() {
    need localmount
    after bootmisc
}

start_pre() {
    # Seed the tmpfs from the tar packed as the build's last guest step
    if ! mountpoint -q /etc/ssl/certs 2>/dev/null; then
        mkdir -p /run/e2b/certs
        if tar -C /run/e2b/certs -xf /usr/local/share/e2b/ssl-certs.tar 2>/dev/null || \
           cp -a /etc/ssl/certs/. /run/e2b/certs/ 2>/dev/null; then
            mount --bind /run/e2b/certs /etc/ssl/certs
        fi
        [ -s /etc/ssl/certs/ca-certificates.crt ] || update-ca-certificates 2>/dev/null || true
    fi
}
