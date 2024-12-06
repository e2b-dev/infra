package network

import (
	"fmt"
	"log"
	"net"

	"github.com/vishvananda/netlink"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Host loopback interface name
const loopbackInterface = "lo"

// Host default gateway name
var defaultGateway = utils.Must(getDefaultGateway())

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
func getDefaultGateway() (string, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return "", fmt.Errorf("error fetching routes: %w", err)
	}

	x := net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 0)}
	for _, route := range routes {
		// 0.0.0.0/0
		log.Printf("Route.Dst: IP:%d\n, MASK:%d", route.Dst.IP, route.Dst.Mask)
		log.Printf("Route.Gw: %+v\n", route.Gw)
		log.Printf("Looking for: IP:%d\n, MASK:%d", x.IP, x.Mask)

		log.Printf("=====================================")
		if route.Dst.String() == "0.0.0.0/0" && route.Gw != nil {
			log.Printf("default gateway: %s", route.Gw.String())
			link, linkErr := netlink.LinkByIndex(route.LinkIndex)
			if linkErr != nil {
				return "", fmt.Errorf("error fetching interface for default gateway: %w", linkErr)
			}

			return link.Attrs().Name, nil
		}
	}

	return "", fmt.Errorf("cannot find default gateway")
}
