package tcpfirewall

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// getOriginalDst retrieves the original destination IP and port before DNAT was applied.
// This uses the SO_ORIGINAL_DST socket option which is a stable Linux kernel API.
func getOriginalDst(conn net.Conn) (net.IP, int, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, 0, fmt.Errorf("not a TCP connection")
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return nil, 0, err
	}

	var ip net.IP
	var port int
	var sockErr error

	// SO_ORIGINAL_DST returns the original destination before iptables DNAT
	const soOriginalDst = 80 // Linux: SO_ORIGINAL_DST

	err = rawConn.Control(func(fd uintptr) {
		// IPv4: returns sockaddr_in (16 bytes)
		var addr [16]byte
		addrLen := uint32(len(addr))

		_, _, errno := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT, fd,
			syscall.SOL_IP, soOriginalDst,
			uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&addrLen)), 0,
		)
		if errno != 0 {
			sockErr = errno

			return
		}

		// sockaddr_in layout: family(2) + port(2 big-endian) + addr(4) + zero(8)
		port = int(addr[2])<<8 | int(addr[3])
		ip = net.IPv4(addr[4], addr[5], addr[6], addr[7])
	})
	if err != nil {
		return nil, 0, err
	}

	return ip, port, sockErr
}
