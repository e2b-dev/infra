{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/e2b-ipv4-forward.service" 0o644 }}

[Unit]
Description=E2B IPv4 PREROUTING DNAT to 127.0.0.1
After=network-pre.target
DefaultDependencies=no

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'echo 1 > /proc/sys/net/ipv4/conf/all/route_localnet'
ExecStart=/bin/sh -c 'iptables -t nat -C PREROUTING -i eth0 -p tcp -j DNAT --to-destination 127.0.0.1 2>/dev/null || iptables -t nat -A PREROUTING -i eth0 -p tcp -j DNAT --to-destination 127.0.0.1'

[Install]
WantedBy=multi-user.target
