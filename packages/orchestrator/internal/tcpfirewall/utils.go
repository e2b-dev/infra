package tcpfirewall

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// getOriginalDstPort retrieves the original destination port before DNAT was applied.
// This uses the SO_ORIGINAL_DST socket option which is a stable Linux kernel API.
func getOriginalDstPort(conn net.Conn) (int, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return 0, fmt.Errorf("not a TCP connection")
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return 0, err
	}

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
	})
	if err != nil {
		return 0, err
	}

	return port, sockErr
}

// getOriginalDstIP retrieves the original destination IP before DNAT was applied.
// This uses the SO_ORIGINAL_DST socket option which is a stable Linux kernel API.
func getOriginalDstIP(conn net.Conn) (net.IP, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, fmt.Errorf("not a TCP connection")
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return nil, err
	}

	var ip net.IP
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
		ip = net.IPv4(addr[4], addr[5], addr[6], addr[7])
	})
	if err != nil {
		return nil, err
	}

	return ip, sockErr
}
