package network

import (
	"fmt"
	"net"
	"reflect"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	// Using reserver IPv4 in range that is used for experiments and documentation
	// https://en.wikipedia.org/wiki/Reserved_IP_addresses
	HyperloopIPAddress       string `env:"SANDBOX_HYPERLOOP_IP"         envDefault:"192.0.2.1"`
	HyperloopProxyPort       uint16 `env:"SANDBOX_HYPERLOOP_PROXY_PORT" envDefault:"5010"`
	UseLocalNamespaceStorage bool   `env:"USE_LOCAL_NAMESPACE_STORAGE"`
	LocalNamespaceStorageDir string `env:"LOCAL_NAMESPACE_STORAGE_DIR"  envDefault:"/run/orchestrator/netns"`

	SandboxesHostNetworkCIDR    *net.IPNet `env:"SANDBOXES_HOST_NETWORK_CIDR"    envDefault:"10.11.0.0/16"`
	SandboxesVirtualNetworkCIDR *net.IPNet `env:"SANDBOXES_VIRTUAL_NETWORK_CIDR" envDefault:"10.12.0.0/16"`

	// not set by env, but calculated at runtime
	VirtualSlotSize int
}

func getVirtualSlotsSize(c Config) int {
	ones, _ := c.SandboxesVirtualNetworkCIDR.Mask.Size()

	// total IPs in the CIDR block
	totalIPs := 1 << (32 - ones)

	// total slots that we can allocate
	// we need to divide total IPs by number of addresses per slot (vpeer and veth)
	// then we subtract the number of addresses so it will not overflow, because we are adding them incrementally by slot index
	totalSlots := (totalIPs / vrtAddressPerSlot) - vrtAddressPerSlot

	return totalSlots
}

func ParseConfig() (Config, error) {
	config, err := env.ParseAsWithOptions[Config](env.Options{
		FuncMap: map[reflect.Type]env.ParserFunc{
			reflect.TypeOf(net.IPNet{}): ParseIPNet,
		},
	})
	if err != nil {
		return config, err
	}

	config.VirtualSlotSize = getVirtualSlotsSize(config)
	return config, nil
}

func ParseIPNet(cidr string) (any, error) {
	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse network CIDR %s: %w", cidr, err)
	}

	return *subnet, nil
}
