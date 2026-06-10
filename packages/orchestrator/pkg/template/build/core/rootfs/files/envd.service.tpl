{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/envd.service" 0o644 }}

[Unit]
Description=Env Daemon Service
# Start as early as possible on cold boot: envd only needs journald's socket
# and a writable rootfs; networking is configured by the kernel (ip=) before
# userspace. Default dependencies would gate it on sysinit/basic.target
# (~0.5s), and the previous After=multi-user.target on chrony-wait (~8s).
DefaultDependencies=no
After=systemd-journald.socket systemd-remount-fs.service
Wants=systemd-journald.socket
Conflicts=shutdown.target
Before=shutdown.target
# Disable rate limiting; retry forever
StartLimitIntervalSec=0

[Service]
Type=simple
Restart=always
User=root
Group=root
Environment=GOTRACEBACK=all
LimitCORE=infinity
# Seed the tmpfs from the tar packed at build time (one sequential read);
# fall back to copying the cert dir, then to regenerating the bundle.
ExecStartPre=/bin/sh -c 'mountpoint -q /etc/ssl/certs || { mkdir -p /run/e2b/certs && { tar -C /run/e2b/certs -xf /usr/local/share/e2b/ssl-certs.tar 2>/dev/null || cp -a /etc/ssl/certs/. /run/e2b/certs/ 2>/dev/null; }; mount --bind /run/e2b/certs /etc/ssl/certs; } && ([ -s /etc/ssl/certs/ca-certificates.crt ] || update-ca-certificates)'
ExecStart=/usr/bin/envd
Nice=-20
IOSchedulingClass=realtime
IOSchedulingPriority=4
OOMPolicy=continue
OOMScoreAdjust=-1000
Environment="GOMEMLIMIT={{ .MemoryLimit }}MiB"

Delegate=yes
MemoryMin=50M
MemoryLow=100M
CPUAccounting=yes
CPUWeight=1000
IOAccounting=yes
IOWeight=10000

[Install]
WantedBy=multi-user.target
