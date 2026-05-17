{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/e2b-netns.service" 0o644 }}

[Unit]
Description=E2B envd network namespace setup
DefaultDependencies=no
After=systemd-networkd.service
Requires=systemd-networkd.service
Before=network.target sysinit.target

[Service]
Type=oneshot
RemainAfterExit=yes

# Create the namespace and move eth0 + the envd-side veth peer into it.
# `-` prefix ignores failure so re-runs across reboots are idempotent.
ExecStart=-/sbin/ip netns add envd-ns
ExecStart=-/sbin/ip link set eth0 netns envd-ns
ExecStart=-/sbin/ip link set veth-envd netns envd-ns

# Configure interfaces inside envd-ns (systemd-networkd doesn't run there).
ExecStart=-/sbin/ip -n envd-ns link set lo up
ExecStart=-/sbin/ip -n envd-ns addr add 169.254.0.21/30 dev eth0
ExecStart=-/sbin/ip -n envd-ns link set eth0 up
ExecStart=-/sbin/ip -n envd-ns route add default via 169.254.0.22
ExecStart=-/sbin/ip -n envd-ns addr add 192.168.250.2/30 dev veth-envd
ExecStart=-/sbin/ip -n envd-ns link set veth-envd up
ExecStart=/sbin/ip netns exec envd-ns sysctl -qw net.ipv4.ip_forward=1

# Idempotent iptables: -C check before -A. Wrapped in sh because there is no
# declarative systemd directive for "add rule if not present".
ExecStart=/bin/sh -c 'ip netns exec envd-ns iptables -t nat -C POSTROUTING -o eth0 -j MASQUERADE 2>/dev/null || ip netns exec envd-ns iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE'
ExecStart=/bin/sh -c 'ip netns exec envd-ns iptables -t nat -C PREROUTING -i eth0 -p tcp --dport 49983 -j RETURN 2>/dev/null || ip netns exec envd-ns iptables -t nat -A PREROUTING -i eth0 -p tcp --dport 49983 -j RETURN'
ExecStart=/bin/sh -c 'ip netns exec envd-ns iptables -t nat -C PREROUTING -i eth0 -p tcp -j DNAT --to-destination 192.168.250.1 2>/dev/null || ip netns exec envd-ns iptables -t nat -A PREROUTING -i eth0 -p tcp -j DNAT --to-destination 192.168.250.1'

[Install]
WantedBy=sysinit.target
