//go:build !linux
// +build !linux

package network

import (
	"errors"

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
	return "", errors.New("platform does not support default gateway")
}
