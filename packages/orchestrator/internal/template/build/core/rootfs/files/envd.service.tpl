{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/envd.service" 0o644 }}

[Unit]
Description=Env Daemon Service
After=multi-user.target

[Service]
Type=simple
Restart=always
User=root
Group=root
Environment=GOTRACEBACK=all
LimitCORE=infinity
ExecStart=/bin/bash -l -c "/usr/bin/envd"
OOMPolicy=continue
OOMScoreAdjust=-1000
Environment="GOMEMLIMIT={{ .MemoryLimit }}MiB"
StandardOutput=null
StandardError=null

Delegate=yes
MemoryMin=50M
MemoryLow=100M
CPUAccounting=yes
CPUWeight=1000

[Install]
WantedBy=multi-user.target
