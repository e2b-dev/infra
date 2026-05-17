{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/e2b-netns.service" 0o644 }}

[Unit]
Description=E2B envd network namespace setup
DefaultDependencies=no
Before=network-pre.target sysinit.target
After=systemd-tmpfiles-setup-dev.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/e2b-netns-setup.sh

[Install]
WantedBy=sysinit.target
