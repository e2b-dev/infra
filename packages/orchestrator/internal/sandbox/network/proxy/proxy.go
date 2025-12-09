package proxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"go.uber.org/zap"
	"inet.af/tcpproxy"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	// upstreamDialTimeout is the maximum time to wait for upstream connections.
	// This prevents goroutine leaks from slow/unresponsive DNS or connections.
	upstreamDialTimeout = 30 * time.Second
)

// OriginalDstConn is implemented by connections that know their original destination
type OriginalDstConn interface {
	OriginalDstPort() int
}

// origDstConn wraps a connection with its original destination port
type origDstConn struct {
	net.Conn

	port int
}

func (c *origDstConn) OriginalDstPort() int { return c.port }

type Proxy struct {
	logger    logger.Logger
	sandboxes *sandbox.Map

	listenPort uint16

	proxy  *tcpproxy.Proxy
	cancel context.CancelFunc
}

func New(ctx context.Context, logger logger.Logger, port uint16, sandboxes *sandbox.Map) *Proxy {
	return &Proxy{
		listenPort: port,
		logger:     logger,
		sandboxes:  sandboxes,
	}
}

func (p *Proxy) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	p.proxy = &tcpproxy.Proxy{}
	addr := fmt.Sprintf("0.0.0.0:%d", p.listenPort)

	// Custom listener that wraps connections with their original destination
	p.proxy.ListenFunc = func(network, laddr string) (net.Listener, error) {
		ln, err := net.Listen(network, laddr)
		if err != nil {
			return nil, err
		}

		return &origDstListener{Listener: ln, logger: p.logger, ctx: ctx}, nil
	}

	// TLS target for SNI-based routing
	tlsTarget := &allowlistTarget{proxy: p, logger: p.logger, ctx: ctx}

	// HTTP target for Host header-based routing
	httpTarget := &allowlistTarget{proxy: p, logger: p.logger, ctx: ctx}

	// Route all TLS traffic through our allowlist target
	p.proxy.AddSNIMatchRoute(addr, func(_ context.Context, _ string) bool { return true }, tlsTarget)

	// Route all HTTP traffic through our allowlist target
	p.proxy.AddHTTPHostMatchRoute(addr, func(_ context.Context, _ string) bool { return true }, httpTarget)

	// Block unrecognized protocols
	p.proxy.AddRoute(addr, &blockTarget{logger: p.logger, ctx: ctx})

	p.logger.Info(ctx, "Proxy started", zap.String("address", addr))

	go func() {
		<-ctx.Done()
		p.proxy.Close()
	}()

	return p.proxy.Run()
}

func (p *Proxy) Close(ctx context.Context) error {
	if p.cancel != nil {
		p.cancel()
	}

	return nil
}

// origDstListener wraps accepted connections with their original destination port
type origDstListener struct {
	net.Listener

	logger logger.Logger
	ctx    context.Context
}

func (l *origDstListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	port := 0
	if p, err := getOriginalDstPort(conn); err == nil {
		port = p
	}

	return &origDstConn{Conn: conn, port: port}, nil
}

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

// getOrigDstPort extracts the original port from a (possibly wrapped) connection
func getOrigDstPort(conn net.Conn) (int, error) {
	// Walk the wrapper chain looking for OriginalDstConn
	for c := conn; c != nil; {
		if oc, ok := c.(OriginalDstConn); ok && oc.OriginalDstPort() != 0 {
			return oc.OriginalDstPort(), nil
		}

		// Try to unwrap - tcpproxy.Conn has Conn as a public field
		if tc, ok := c.(*tcpproxy.Conn); ok {
			c = tc.Conn
		} else {
			break
		}
	}

	return 0, fmt.Errorf("original destination port not found")
}

var _ tcpproxy.Target = &allowlistTarget{}

type allowlistTarget struct {
	proxy  *Proxy
	logger logger.Logger
	ctx    context.Context
}

func (t *allowlistTarget) HandleConn(conn net.Conn) {
	sourceAddr := conn.RemoteAddr().String()

	// Get hostname from tcpproxy's wrapped connection
	var hostname string
	if tc, ok := conn.(*tcpproxy.Conn); ok {
		hostname = tc.HostName // tcpproxy.Conn has HostName as a public field
	}

	if hostname == "" {
		t.logger.Debug(t.ctx, "No hostname found, blocking", zap.String("source_addr", sourceAddr))
		conn.Close()

		return
	}

	// Check allowlist
	allowed, err := t.proxy.isAllowed(sourceAddr, hostname)
	if err != nil {
		t.logger.Error(t.ctx, "Allowlist check failed", zap.Error(err))
		conn.Close()

		return
	}

	if !allowed {
		t.logger.Debug(t.ctx, "Blocked connection",
			zap.String("hostname", hostname),
			zap.String("source_addr", sourceAddr),
		)
		conn.Close()

		return
	}

	// Get original destination port from wrapped connection
	dstPort, err := getOrigDstPort(conn)
	if err != nil {
		t.logger.Error(t.ctx, "Failed to get original destination port", zap.Error(err))
		conn.Close()

		return
	}

	upstreamAddr := net.JoinHostPort(hostname, fmt.Sprintf("%d", dstPort))

	t.logger.Debug(t.ctx, "Proxying connection",
		zap.String("source_addr", sourceAddr),
		zap.String("upstream_addr", upstreamAddr),
	)

	// Delegate to tcpproxy's built-in proxying (handles dial + bidirectional copy)
	// Use a dial timeout to prevent goroutine leaks from slow/unresponsive upstreams
	dp := &tcpproxy.DialProxy{
		Addr:        upstreamAddr,
		DialTimeout: upstreamDialTimeout,
	}
	dp.HandleConn(conn)
}

var _ tcpproxy.Target = &blockTarget{}

type blockTarget struct {
	logger logger.Logger
	ctx    context.Context
}

func (t *blockTarget) HandleConn(conn net.Conn) {
	conn.Close()
	t.logger.Debug(t.ctx, "Blocked unrecognized protocol",
		zap.String("remote_addr", conn.RemoteAddr().String()),
	)
}

func (p *Proxy) isAllowed(sourceAddr string, hostname string) (bool, error) {
	sbx, err := p.sandboxes.GetByHostPort(sourceAddr)
	if err != nil {
		return false, err
	}

	if net := sbx.Config.Network; net != nil {
		for _, domain := range net.GetEgress().GetAllowedDomains() {
			if strings.EqualFold(domain, hostname) {
				return true, nil
			}
			if strings.HasPrefix(domain, "*.") {
				suffix := domain[1:]
				if strings.HasSuffix(strings.ToLower(hostname), strings.ToLower(suffix)) {
					return true, nil
				}
			}
		}
	}

	return false, nil
}
