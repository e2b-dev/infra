//go:build !linux

package port

import gnet "github.com/shirou/gopsutil/v4/net"

// listListeningSockets is only used on Linux; the rest of envd doesn't run
// the port forwarder elsewhere. Returning empty keeps the build green on
// developer macOS without pulling in a platform-specific fallback.
func listListeningSockets() ([]gnet.ConnectionStat, error) {
	return nil, nil
}
