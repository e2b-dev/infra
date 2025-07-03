package port

import (
	"slices"

	net "github.com/shirou/gopsutil/v4/net"
)

type ScannerFilter struct {
	State string
	IPs   []string
}

func (sf *ScannerFilter) Match(proc *net.ConnectionStat) bool {
	// Filter is an empty struct.
	if sf.State == "" && len(sf.IPs) == 0 {
		return false
	}

	ipMatch := slices.Contains(sf.IPs, proc.Laddr.IP)

	if ipMatch && sf.State == proc.Status {
		return true
	}

	return false
}
