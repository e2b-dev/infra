package hostfilter

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

// connMeta holds per-connection metadata (context for tracing, original destination port)
type connMeta struct {
	net.Conn

	port int
	ctx  context.Context //nolint:containedctx // intentional: carries per-connection context for request tracing
}

func (c *connMeta) OriginalDstPort() int { return c.port }

func (c *connMeta) Context() context.Context { return c.ctx }

type Proxy struct {
	logger    logger.Logger
	sandboxes *sandbox.Map

	listenPort uint16

	proxy  *tcpproxy.Proxy
	cancel context.CancelFunc
}

func New(logger logger.Logger, port uint16, sandboxes *sandbox.Map) *Proxy {
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
		var lc net.ListenConfig
		ln, err := lc.Listen(ctx, network, laddr)
		if err != nil {
			return nil, err
		}

		return &origDstListener{Listener: ln, ctx: ctx}, nil
	}

	// Route all TLS traffic through allowlist (SNI-based routing)
	p.proxy.AddSNIMatchRoute(addr, func(_ context.Context, _ string) bool { return true }, targetFunc(p.allowlistHandler))

	// Route all HTTP traffic through allowlist (Host header-based routing)
	p.proxy.AddHTTPHostMatchRoute(addr, func(_ context.Context, _ string) bool { return true }, targetFunc(p.allowlistHandler))

	// Block unrecognized protocols
	p.proxy.AddRoute(addr, targetFunc(p.blockHandler))

	p.logger.Info(ctx, "Proxy started", zap.String("address", addr))

	go func() {
		<-ctx.Done()
		p.proxy.Close()
	}()

	return p.proxy.Run()
}

func (p *Proxy) Close(_ context.Context) error {
	if p.cancel != nil {
		p.cancel()
	}

	return nil
}

// origDstListener wraps accepted connections with metadata (original destination port + context)
type origDstListener struct {
	net.Listener

	ctx context.Context //nolint:containedctx // propagated to connections for request tracing
}

func (l *origDstListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	port, err := getOriginalDstPort(conn)
	if err != nil {
		conn.Close()

		return nil, err
	}

	return &connMeta{Conn: conn, port: port, ctx: l.ctx}, nil
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

// unwrapConnMeta extracts our connection metadata from a (possibly wrapped) connection.
func unwrapConnMeta(conn net.Conn) (*connMeta, bool) {
	for c := conn; c != nil; {
		if cm, ok := c.(*connMeta); ok {
			return cm, true
		}

		// Try to unwrap - tcpproxy.Conn has Conn as a public field
		if tc, ok := c.(*tcpproxy.Conn); ok {
			c = tc.Conn
		} else {
			break
		}
	}

	return nil, false
}

// targetFunc adapts a handler function to tcpproxy.Target interface.
// It extracts connection metadata and passes it to the handler with a clean signature.
type targetFunc func(ctx context.Context, conn net.Conn, dstPort int)

func (f targetFunc) HandleConn(conn net.Conn) {
	meta, ok := unwrapConnMeta(conn)
	if !ok {
		conn.Close()

		return
	}

	f(meta.Context(), conn, meta.OriginalDstPort())
}

func (p *Proxy) allowlistHandler(ctx context.Context, conn net.Conn, dstPort int) {
	sourceAddr := conn.RemoteAddr().String()

	// Get hostname from tcpproxy's wrapped connection
	var hostname string
	if tc, ok := conn.(*tcpproxy.Conn); ok {
		hostname = tc.HostName
	}

	if hostname == "" {
		p.logger.Debug(ctx, "No hostname found, blocking", zap.String("source_addr", sourceAddr))
		conn.Close()

		return
	}

	allowed, err := p.isAllowed(sourceAddr, hostname)
	if err != nil {
		p.logger.Error(ctx, "Allowlist check failed", zap.Error(err))
		conn.Close()

		return
	}

	if !allowed {
		p.logger.Debug(ctx, "Blocked connection",
			zap.String("hostname", hostname),
			zap.String("source_addr", sourceAddr),
		)
		conn.Close()

		return
	}

	upstreamAddr := net.JoinHostPort(hostname, fmt.Sprintf("%d", dstPort))

	p.logger.Debug(ctx, "Proxying connection",
		zap.String("source_addr", sourceAddr),
		zap.String("upstream_addr", upstreamAddr),
	)

	dp := &tcpproxy.DialProxy{
		Addr:        upstreamAddr,
		DialTimeout: upstreamDialTimeout,
	}
	dp.HandleConn(conn)
}

func (p *Proxy) blockHandler(ctx context.Context, conn net.Conn, _ int) {
	conn.Close()
	remoteAddr := conn.RemoteAddr().String()

	p.logger.Debug(ctx, "Blocked unrecognized protocol",
		zap.String("remote_addr", remoteAddr),
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
