package network

import (
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/vishvananda/netlink"
)

const loNS = "lo"

var hostDefaultGateway = utils.Must(getDefaultGateway())

func getDefaultGateway() (string, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return "", fmt.Errorf("error fetching routes: %w", err)
	}

	for _, route := range routes {
		if route.Dst == nil && route.Gw != nil {
			link, linkErr := netlink.LinkByIndex(route.LinkIndex)
			if linkErr != nil {
				return "", fmt.Errorf("error fetching interface for default gateway: %w", linkErr)
			}

			return link.Attrs().Name, nil
		}
	}

	return "", fmt.Errorf("cannot find default gateway")
}
