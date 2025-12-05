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
ExecStart=/bin/bash -l -c "/usr/bin/envd -parent-cgroup=envd.slice -cmd-cgroup=commands.slice"
Environment="GOMEMLIMIT={{ .MemoryLimit }}MiB"

Slice=envd.slice
LimitCORE=infinity
Delegate=yes
OOMPolicy=continue
OOMScoreAdjust=-1000
CPUAccounting=yes

[Install]
WantedBy=multi-user.target
