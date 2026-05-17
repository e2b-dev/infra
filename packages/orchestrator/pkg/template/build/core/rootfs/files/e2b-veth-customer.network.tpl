{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/network/10-e2b-veth-customer.network" 0o644 }}

[Match]
Name=veth-customer

[Network]
Address=192.168.250.1/30
Gateway=192.168.250.2
