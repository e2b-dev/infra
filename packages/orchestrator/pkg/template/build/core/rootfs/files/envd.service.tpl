{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/envd.service" 0o644 }}

[Unit]
Description=Env Daemon Service
After=multi-user.target e2b-netns.service
Requires=e2b-netns.service
# Disable rate limiting; retry forever
StartLimitIntervalSec=0

[Service]
Type=simple
Restart=always
User=root
Group=root
# Run envd inside the dedicated network namespace set up by e2b-netns.service.
# Customer iptables in the default namespace cannot reach this namespace.
NetworkNamespacePath=/run/netns/envd-ns
Environment=GOTRACEBACK=all
LimitCORE=infinity
ExecStartPre=/bin/sh -c 'mountpoint -q /etc/ssl/certs || (mkdir -p /run/e2b/certs && mount --bind /run/e2b/certs /etc/ssl/certs) && ([ -s /etc/ssl/certs/ca-certificates.crt ] || update-ca-certificates)'
ExecStart=/bin/bash -l -c "/usr/bin/envd"
Nice=-20
OOMPolicy=continue
OOMScoreAdjust=-1000
Environment="GOMEMLIMIT={{ .MemoryLimit }}MiB"

Delegate=yes
MemoryMin=50M
MemoryLow=100M
CPUAccounting=yes
CPUWeight=1000

[Install]
WantedBy=multi-user.target
