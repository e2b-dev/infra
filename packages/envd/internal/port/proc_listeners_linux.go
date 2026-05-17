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

// listListeningSockets returns the TCP and TCP6 sockets in LISTEN state by
// parsing /proc/net/tcp{,6} directly. It is dramatically cheaper than
// gopsutil's net.Connections, which walks every /proc/<pid>/fd directory to
// produce inode→PID mappings (O(processes × open fds) syscalls per scan). The
// port forwarder only needs (family, local addr, local port, status) so the
// PID lookup is pure overhead.
//
// The returned slice is shaped as []net.ConnectionStat for compatibility with
// the existing subscriber/filter wiring; the Pid field is always 0.
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

// tcpStateListen is the on-the-wire encoding of TCP_LISTEN in /proc/net/tcp.
const tcpStateListen = "0A"

// listenStatus is the human-readable status string the rest of the package
// expects (matches gopsutil's net.Connections output).
const listenStatus = "LISTEN"

func parseProcNetTCP(path string, family uint32) ([]gnet.ConnectionStat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []gnet.ConnectionStat
	scanner := bufio.NewScanner(f)
	// Skip header.
	if scanner.Scan() {
		_ = scanner.Text()
	}

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// Need at least: sl, local, rem, st
		if len(fields) < 4 {
			continue
		}
		if fields[3] != tcpStateListen {
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

// decodeProcAddr parses one local_address / remote_address field from
// /proc/net/tcp{,6}: an upper-case hex IP, ':', and a 4-hex-digit port.
//
// The IP bytes are written byte-by-byte but each 4-byte word is in CPU byte
// order (little-endian on x86/arm64). For IPv4 we just reverse the four bytes;
// for IPv6 each consecutive 4-byte group is little-endian within the group.
func decodeProcAddr(s string, family uint32) (string, uint16, error) {
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return "", 0, fmt.Errorf("missing colon in %q", s)
	}
	ipHex := s[:colon]
	portHex := s[colon+1:]

	port64, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return "", 0, fmt.Errorf("port %q: %w", portHex, err)
	}

	raw, err := hex.DecodeString(ipHex)
	if err != nil {
		return "", 0, fmt.Errorf("ip %q: %w", ipHex, err)
	}

	switch family {
	case syscall.AF_INET:
		if len(raw) != 4 {
			return "", 0, fmt.Errorf("ipv4 expects 4 bytes, got %d", len(raw))
		}
		ip := net.IPv4(raw[3], raw[2], raw[1], raw[0])

		return ip.String(), uint16(port64), nil
	case syscall.AF_INET6:
		if len(raw) != 16 {
			return "", 0, fmt.Errorf("ipv6 expects 16 bytes, got %d", len(raw))
		}
		buf := make([]byte, 16)
		// Reverse each 4-byte group.
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
