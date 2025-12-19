package network

import "github.com/caarlos0/env/v11"

type Config struct {
	// Using reserver IPv4 in range that is used for experiments and documentation
	// https://en.wikipedia.org/wiki/Reserved_IP_addresses
	HyperloopIPAddress       string `env:"SANDBOX_HYPERLOOP_IP"         envDefault:"192.0.2.1"`
	HyperloopProxyPort       uint16 `env:"SANDBOX_HYPERLOOP_PROXY_PORT" envDefault:"5010"`
	UseLocalNamespaceStorage bool   `env:"USE_LOCAL_NAMESPACE_STORAGE"`

	// Network slots
	NetworkSlotsReusePoolSize int `env:"NETWORK_SLOTS_REUSE_POOL_SIZE" envDefault:"8"`
	NetworkSlotsFreshPoolSize int `env:"NETWORK_SLOTS_FRESH_POOL_SIZE" envDefault:"8"`

	// SandboxTCPFirewallPort is the port to redirect TCP traffic to for egress filtering
	SandboxTCPFirewallPort uint16 `env:"SANDBOX_TCP_FIREWALL_PORT" envDefault:"5016"`
}

func ParseConfig() (Config, error) {
	return env.ParseAs[Config]()
}
