//go:build linux

package port

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	gnet "github.com/shirou/gopsutil/v4/net"
)

// listListeningSockets parses /proc/net/tcp{,6} and returns LISTEN sockets.
// Skips the /proc/<pid>/fd walk that gopsutil.net.Connections does — the
// forwarder doesn't use PID.
func listListeningSockets() ([]gnet.ConnectionStat, error) {
	var out []gnet.ConnectionStat

	if v4, err := parseProcNetTCP("/proc/net/tcp", syscall.AF_INET); err == nil {
		out = append(out, v4...)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	if v6, err := parseProcNetTCP("/proc/net/tcp6", syscall.AF_INET6); err == nil {
		out = append(out, v6...)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return out, nil
}

const (
	tcpStateListen = "0A"
	listenStatus   = "LISTEN"
)

func parseProcNetTCP(path string, family uint32) ([]gnet.ConnectionStat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []gnet.ConnectionStat
	scanner := bufio.NewScanner(f)
	if scanner.Scan() { // skip header
		_ = scanner.Text()
	}

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[3] != tcpStateListen {
			continue
		}

		ip, port, err := decodeProcAddr(fields[1], family)
		if err != nil {
			continue
		}

		out = append(out, gnet.ConnectionStat{
			Family: family,
			Type:   syscall.SOCK_STREAM,
			Laddr:  gnet.Addr{IP: ip, Port: uint32(port)},
			Status: listenStatus,
		})
	}

	return out, scanner.Err()
}

// decodeProcAddr parses an IIIIIIII:PPPP (or 32-char-hex:PPPP for v6) field.
// Each 4-byte word in the IP is in CPU byte order.
func decodeProcAddr(s string, family uint32) (string, uint16, error) {
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return "", 0, fmt.Errorf("missing colon in %q", s)
	}

	port64, err := strconv.ParseUint(s[colon+1:], 16, 16)
	if err != nil {
		return "", 0, err
	}

	raw, err := hex.DecodeString(s[:colon])
	if err != nil {
		return "", 0, err
	}

	switch family {
	case syscall.AF_INET:
		if len(raw) != 4 {
			return "", 0, fmt.Errorf("ipv4 expects 4 bytes, got %d", len(raw))
		}
		return net.IPv4(raw[3], raw[2], raw[1], raw[0]).String(), uint16(port64), nil
	case syscall.AF_INET6:
		if len(raw) != 16 {
			return "", 0, fmt.Errorf("ipv6 expects 16 bytes, got %d", len(raw))
		}
		buf := make([]byte, 16)
		for i := 0; i < 16; i += 4 {
			buf[i+0] = raw[i+3]
			buf[i+1] = raw[i+2]
			buf[i+2] = raw[i+1]
			buf[i+3] = raw[i+0]
		}
		return net.IP(buf).String(), uint16(port64), nil
	default:
		return "", 0, fmt.Errorf("unsupported family %d", family)
	}
}
