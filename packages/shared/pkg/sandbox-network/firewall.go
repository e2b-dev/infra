package sandbox_network

import (
	"fmt"
	"strings"

	"github.com/ngrok/firewall_toolkit/pkg/set"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	AllInternetTrafficCIDR = "0.0.0.0/0"
)

var DeniedSandboxCIDRs = []string{
	"10.0.0.0/8",
	"169.254.0.0/16",
	"192.168.0.0/16",
	"172.16.0.0/12",
}

var deniedSandboxData = utils.Must(set.AddressStringsToSetData(DeniedSandboxCIDRs))

// CanAllowCIDR checks if the address is in the default denied ranges.
func CanAllowCIDR(cidr string) error {
	if cidr == AllInternetTrafficCIDR {
		// Internet is enabled by default.
		return fmt.Errorf("internet is enabled by default")
	}

	addressData, err := set.AddressStringsToSetData([]string{cidr})
	if err != nil {
		return err
	}

	if len(addressData) == 0 {
		return fmt.Errorf("address %s is not a valid IP address", cidr)
	}

	for _, deniedRange := range deniedSandboxData {
		if deniedRange.Prefix.Overlaps(addressData[0].Prefix) {
			return fmt.Errorf("address %s is blocked by the provider and cannot be added to the allow list", cidr)
		}
	}

	return nil
}

// AddressStringToCIDR converts a string address to the CIDR format.
// Supports only IPv4 addresses.
func AddressStringToCIDR(addressString string) string {
	if !strings.Contains(addressString, "/") {
		addressString += "/32"
	}

	return addressString
}

// AddressStringsToCIDRs converts a list of string addresses to the CIDR format.
// Supports only IPv4 addresses.
func AddressStringsToCIDRs(addressStrings []string) []string {
	data := make([]string, 0, len(addressStrings))

	for _, addressString := range addressStrings {
		data = append(data, AddressStringToCIDR(addressString))
	}

	return data
}
