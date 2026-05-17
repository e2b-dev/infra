{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/network/10-e2b-veth.netdev" 0o644 }}

[NetDev]
Name=veth-customer
Kind=veth

[Peer]
Name=veth-envd
