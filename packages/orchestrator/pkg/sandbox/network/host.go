//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"

	"github.com/vishvananda/netlink"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Host loopback interface name
const loopbackInterface = "lo"

// DSCP value applied to all packets egressing a sandbox netns.
// CS1 (8) is the standardized "Scavenger" / lower-than-best-effort class
// (RFC 3662) — signals untrusted, low-priority traffic, and gives operators
// a stable L3 marker that survives across protocols (TCP/UDP/ICMP/etc.) so
// downstream firewalls can identify sandbox traffic without relying on L7.
const sandboxEgressDSCP = "8"

// Host default gateway name
var defaultGateway = utils.Must(getDefaultGateway(context.Background()))

//	func getDefaultGateway() (string, error) {
//		route, err := exec.Command(
//			"sh",
//			"-c",
//			"ip route show default | awk '{print $5}'",
//		).Output()
//		if err != nil {
//			return "", fmt.Errorf("error fetching default gateway: %w", err)
//		}
//
//		return string(route), nil
//	}
func getDefaultGateway(ctx context.Context) (string, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return "", fmt.Errorf("error fetching routes: %w", err)
	}

	for _, route := range routes {
		// 0.0.0.0/0
		if route.Dst.String() == "0.0.0.0/0" && route.Gw != nil {
			logger.L().Info(ctx, "default gateway", zap.String("gateway", route.Gw.String()))

			link, linkErr := netlink.LinkByIndex(route.LinkIndex)

			if linkErr != nil {
				return "", fmt.Errorf("error fetching interface for default gateway: %w", linkErr)
			}

			return link.Attrs().Name, nil
		}
	}

	return "", errors.New("cannot find default gateway")
}
