{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/usr/local/bin/e2b-netns-setup.sh" 0o755 }}
#!/bin/sh
# Set up a dedicated network namespace for envd so customer iptables changes
# in the default namespace cannot break envd's MMDS / /init path.
#
# Topology:
#   default ns (customer)            envd-ns
#     veth-customer (192.168.250.1)    veth-envd  (192.168.250.2)
#                                      eth0       (169.254.0.21, moved in)
#
# Inbound traffic to 169.254.0.21 lands in envd-ns. Port 49983 (envd) bypasses
# NAT; everything else is DNAT'd to 192.168.250.1, picked up by veth-customer
# and the existing PREROUTING DNAT to 127.0.0.1.
# Outbound customer traffic uses 192.168.250.2 as gateway and is MASQUERADE'd
# in envd-ns to 169.254.0.21.

set -eu

NS=envd-ns
GUEST_IP=169.254.0.21
GUEST_MASK=30
GW=169.254.0.22
VETH_ENVD_IP=192.168.250.2
VETH_HOST_IP=192.168.250.1
ENVD_PORT=49983

# Idempotent.
if ip netns list | grep -q "^${NS}\b"; then
  exit 0
fi

ip netns add "${NS}"

# Move eth0 into envd-ns and reapply its IP/route.
ip link set eth0 netns "${NS}"
ip -n "${NS}" link set lo up
ip -n "${NS}" addr add "${GUEST_IP}/${GUEST_MASK}" dev eth0
ip -n "${NS}" link set eth0 up
ip -n "${NS}" route add default via "${GW}"

# veth pair between default ns and envd-ns.
ip link add veth-customer type veth peer name veth-envd
ip link set veth-envd netns "${NS}"
ip addr add "${VETH_HOST_IP}/30" dev veth-customer
ip link set veth-customer up
ip route add default via "${VETH_ENVD_IP}"
ip -n "${NS}" addr add "${VETH_ENVD_IP}/30" dev veth-envd
ip -n "${NS}" link set veth-envd up

# envd-ns acts as router for the default ns.
ip netns exec "${NS}" sysctl -qw net.ipv4.ip_forward=1
ip netns exec "${NS}" iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE

# Bypass NAT for the envd port so the orchestrator's /init reaches envd directly.
ip netns exec "${NS}" iptables -t nat -A PREROUTING -i eth0 -p tcp --dport "${ENVD_PORT}" -j RETURN
# Forward all other inbound TCP to the customer-side veth peer.
ip netns exec "${NS}" iptables -t nat -A PREROUTING -i eth0 -p tcp -j DNAT --to-destination "${VETH_HOST_IP}"
