package hostfilter

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"inet.af/tcpproxy"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

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

		return newOrigDstListener(ctx, ln, p.sandboxes, p.logger), nil
	}

	// Route all TLS traffic through allowlist (SNI-based routing)
	p.proxy.AddSNIMatchRoute(addr, func(_ context.Context, _ string) bool { return true }, targetFunc(domainHandler))

	// Route all HTTP traffic through allowlist (Host header-based routing)
	p.proxy.AddHTTPHostMatchRoute(addr, func(_ context.Context, _ string) bool { return true }, targetFunc(domainHandler))

	// Block unrecognized protocols
	p.proxy.AddRoute(addr, targetFunc(cidrOnlyHandler))

	p.logger.Info(ctx, "Host filter proxy started", zap.String("address", addr))

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

// targetFunc adapts a handler function to tcpproxy.Target interface.
type targetFunc func(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger)

func (f targetFunc) HandleConn(conn net.Conn) {
	meta, ok := unwrapConnMeta(conn)
	if !ok {
		conn.Close()

		return
	}

	f(meta.ctx, conn, meta.ip, meta.port, meta.sbx, meta.logger)
}

// origDstListener wraps accepted connections with metadata
type origDstListener struct {
	net.Listener

	sandboxes *sandbox.Map
	logger    logger.Logger

	ctx context.Context //nolint:containedctx // propagated to connections for request tracing
}

func newOrigDstListener(ctx context.Context, listener net.Listener, sandboxes *sandbox.Map, logger logger.Logger) *origDstListener {
	return &origDstListener{Listener: listener, ctx: ctx, sandboxes: sandboxes, logger: logger}
}

func (l *origDstListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	ip, err := getOriginalDstIP(conn)
	if err != nil {
		conn.Close()

		return nil, err
	}

	port, err := getOriginalDstPort(conn)
	if err != nil {
		conn.Close()

		return nil, err
	}

	sourceAddr := conn.RemoteAddr().String()
	sbx, err := l.sandboxes.GetByHostPort(sourceAddr)
	if err != nil {
		conn.Close()

		return nil, err
	}

	logger := l.logger.With(logger.WithSandboxID(sbx.Runtime.SandboxID))

	return &connMeta{Conn: conn, ip: ip, port: port, ctx: l.ctx, sbx: sbx, logger: logger}, nil
}
