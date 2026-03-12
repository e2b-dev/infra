package v2

import (
	"context"
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// WireGuardPeer represents a single WireGuard peer.
type WireGuardPeer struct {
	PublicKey  string
	Endpoint  string   // "host:port"
	AllowedIPs []string // CIDRs
}

// WireGuardConfig configures a WireGuard interface.
type WireGuardConfig struct {
	PrivateKey    string
	ListenPort    int
	InterfaceName string // e.g., "wg0"
	Address       string // e.g., "10.99.0.2/24"
	Peers         []WireGuardPeer
}

// SetupWireGuard creates a WireGuard interface with the given config.
// Uses vishvananda/netlink for link creation and wgctrl for peer configuration.
func SetupWireGuard(ctx context.Context, cfg WireGuardConfig) error {
	// Parse private key
	privKey, err := wgtypes.ParseKey(cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("parse wireguard private key: %w", err)
	}

	// Create WireGuard interface using netlink
	attrs := netlink.NewLinkAttrs()
	attrs.Name = cfg.InterfaceName
	wgLink := &netlink.Wireguard{LinkAttrs: attrs}

	if err := netlink.LinkAdd(wgLink); err != nil {
		return fmt.Errorf("add wireguard link: %w", err)
	}

	// Parse and assign address
	addr, err := netlink.ParseAddr(cfg.Address)
	if err != nil {
		return fmt.Errorf("parse wireguard address: %w", err)
	}

	link, err := netlink.LinkByName(cfg.InterfaceName)
	if err != nil {
		return fmt.Errorf("find wireguard link: %w", err)
	}

	if err := netlink.AddrAdd(link, addr); err != nil {
		return fmt.Errorf("add wireguard address: %w", err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("set wireguard link up: %w", err)
	}

	// Configure WireGuard peers using wgctrl
	client, err := wgctrl.New()
	if err != nil {
		return fmt.Errorf("create wgctrl client: %w", err)
	}
	defer client.Close()

	var peers []wgtypes.PeerConfig
	for _, p := range cfg.Peers {
		pubKey, keyErr := wgtypes.ParseKey(p.PublicKey)
		if keyErr != nil {
			return fmt.Errorf("parse peer public key: %w", keyErr)
		}

		var allowedIPs []net.IPNet
		for _, cidr := range p.AllowedIPs {
			_, ipnet, parseErr := net.ParseCIDR(cidr)
			if parseErr != nil {
				return fmt.Errorf("parse peer allowed IP %s: %w", cidr, parseErr)
			}
			allowedIPs = append(allowedIPs, *ipnet)
		}

		peer := wgtypes.PeerConfig{
			PublicKey:  pubKey,
			AllowedIPs: allowedIPs,
		}

		if p.Endpoint != "" {
			endpoint, resolveErr := net.ResolveUDPAddr("udp", p.Endpoint)
			if resolveErr != nil {
				return fmt.Errorf("resolve peer endpoint %s: %w", p.Endpoint, resolveErr)
			}
			peer.Endpoint = endpoint
		}

		peers = append(peers, peer)
	}

	listenPort := cfg.ListenPort
	wgConfig := wgtypes.Config{
		PrivateKey: &privKey,
		ListenPort: &listenPort,
		Peers:      peers,
	}

	if err := client.ConfigureDevice(cfg.InterfaceName, wgConfig); err != nil {
		return fmt.Errorf("configure wireguard device: %w", err)
	}

	logger.L().Info(ctx, "WireGuard interface configured",
		zap.String("iface", cfg.InterfaceName),
		zap.String("address", cfg.Address),
		zap.Int("peers", len(peers)),
	)

	return nil
}

// TeardownWireGuard removes a WireGuard interface.
func TeardownWireGuard(ifname string) error {
	link, err := netlink.LinkByName(ifname)
	if err != nil {
		return fmt.Errorf("find wireguard link %s: %w", ifname, err)
	}

	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("delete wireguard link %s: %w", ifname, err)
	}

	return nil
}
