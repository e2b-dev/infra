{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "etc/systemd/journald.conf.d/e2b.conf" 0o644 }}

[Journal]
Storage=none
MaxLevelConsole=warning
MaxLevelKMsg=warning
MaxLevelWall=emerg
ForwardToSyslog=no
