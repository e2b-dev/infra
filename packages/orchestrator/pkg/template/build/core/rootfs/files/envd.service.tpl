{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/envd.service" 0o644 }}

[Unit]
Description=Env Daemon Service
After=multi-user.target
# Disable rate limiting; retry forever
StartLimitIntervalSec=0

[Service]
Type=simple
Restart=always
User=root
Group=root
Environment=GOTRACEBACK=all
LimitCORE=infinity
ExecStartPre=/bin/sh -c 'mountpoint -q /etc/ssl/certs || (mkdir -p /run/e2b/certs && mount --bind /run/e2b/certs /etc/ssl/certs) && ([ -s /etc/ssl/certs/ca-certificates.crt ] || update-ca-certificates)'
ExecStart=/bin/bash -l -c "/usr/bin/envd"
Nice=-20
OOMPolicy=continue
# -999 (not -1000): envd is still extremely low priority for the OOM killer but
# not fully immune. If envd ever leaks past memory.max for its cgroup, the
# kernel can recover by killing it; combined with Restart=always +
# StartLimitIntervalSec=0 below this self-heals into a fresh envd instead of
# requiring a manual `pkill envd` recipe.
OOMScoreAdjust=-999
Environment="GOMEMLIMIT={{ .MemoryLimit }}MiB"

Delegate=yes
MemoryMin=50M
MemoryLow=100M
CPUAccounting=yes
CPUWeight=1000

[Install]
WantedBy=multi-user.target
