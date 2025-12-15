package tcpfirewall

import (
	"context"
	"fmt"
	"net"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"inet.af/tcpproxy"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Proxy struct {
	logger    logger.Logger
	sandboxes *sandbox.Map
	metrics   *Metrics

	listenPort uint16

	proxy *tcpproxy.Proxy
}

func New(logger logger.Logger, networkConfig network.Config, sandboxes *sandbox.Map, meterProvider metric.MeterProvider) *Proxy {
	port := networkConfig.SandboxTCPFirewallPort

	return &Proxy{
		listenPort: port,
		logger:     logger,
		sandboxes:  sandboxes,
		metrics:    NewMetrics(meterProvider),
	}
}

func (p *Proxy) Start(ctx context.Context) error {
	p.proxy = &tcpproxy.Proxy{}
	addr := fmt.Sprintf("0.0.0.0:%d", p.listenPort)

	// Custom listener that wraps connections with their original destination
	p.proxy.ListenFunc = func(network, laddr string) (net.Listener, error) {
		var lc net.ListenConfig
		ln, err := lc.Listen(ctx, network, laddr)
		if err != nil {
			return nil, err
		}

		return newOrigDstListener(ctx, ln, p.sandboxes, p.logger, p.metrics), nil
	}

	// Route all TLS traffic through allowlist (SNI-based routing)
	p.proxy.AddSNIMatchRoute(addr, func(_ context.Context, _ string) bool { return true }, newConnectionHandler(domainHandler, ProtocolTLS, p.metrics, p.logger))

	// Route all HTTP traffic through allowlist (Host header-based routing)
	p.proxy.AddHTTPHostMatchRoute(addr, func(_ context.Context, _ string) bool { return true }, newConnectionHandler(domainHandler, ProtocolHTTP, p.metrics, p.logger))

	// Block unrecognized protocols
	p.proxy.AddRoute(addr, newConnectionHandler(cidrOnlyHandler, ProtocolOther, p.metrics, p.logger))

	p.logger.Info(ctx, "Host filter proxy started", zap.String("address", addr))

	go func() {
		<-ctx.Done()
		p.proxy.Close()
	}()

	err := p.proxy.Run()
	if err != nil && strings.Contains(err.Error(), "use of closed network connection") {
		// This is expected when the proxy is closed.
		return nil
	}

	return err
}

func (p *Proxy) Close(_ context.Context) error {
	if p.proxy != nil {
		return p.proxy.Close()
	}

	return nil
}

// handlerFunc is the signature for connection handlers.
type handlerFunc func(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger, metrics *Metrics, protocol Protocol)

var _ tcpproxy.Target = (*connectionHandler)(nil)

// connectionHandler adapts a handler function to tcpproxy.Target interface.
type connectionHandler struct {
	handler  handlerFunc
	protocol Protocol
	metrics  *Metrics
	logger   logger.Logger
}

func newConnectionHandler(handler handlerFunc, protocol Protocol, metrics *Metrics, logger logger.Logger) *connectionHandler {
	return &connectionHandler{
		handler:  handler,
		protocol: protocol,
		metrics:  metrics,
		logger:   logger,
	}
}

func (t *connectionHandler) HandleConn(conn net.Conn) {
	meta, ok := unwrapConnMeta(conn)
	if !ok {
		t.metrics.RecordError(context.Background(), ErrorTypeConnectionMeta, t.protocol)
		conn.Close()

		return
	}

	logger := t.logger.With(logger.WithSandboxID(meta.sbx.Runtime.SandboxID))
	t.metrics.RecordConnection(meta.ctx, t.protocol)

	t.handler(meta.ctx, conn, meta.ip, meta.port, meta.sbx, logger, t.metrics, t.protocol)
}

// origDstListener wraps accepted connections with metadata
type origDstListener struct {
	net.Listener

	sandboxes *sandbox.Map
	logger    logger.Logger
	metrics   *Metrics

	ctx context.Context //nolint:containedctx // propagated to connections for request tracing
}

func newOrigDstListener(ctx context.Context, listener net.Listener, sandboxes *sandbox.Map, logger logger.Logger, metrics *Metrics) *origDstListener {
	return &origDstListener{Listener: listener, ctx: ctx, sandboxes: sandboxes, logger: logger, metrics: metrics}
}

func (l *origDstListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		sourceAddr := conn.RemoteAddr().String()
		sbx, err := l.sandboxes.GetByHostPort(sourceAddr)
		if err != nil {
			l.logger.Error(l.ctx, "failed to find sandbox for connection", zap.String("source", sourceAddr), zap.Error(err))
			l.metrics.RecordError(l.ctx, ErrorTypeSandboxLookup, ProtocolOther)
			conn.Close()

			continue
		}

		ip, port, err := getOriginalDst(conn)
		if err != nil {
			l.logger.Error(l.ctx, "failed to get original destination", zap.Error(err))
			l.metrics.RecordError(l.ctx, ErrorTypeOrigDst, ProtocolOther)
			conn.Close()

			continue
		}

		return &connMeta{Conn: conn, ip: ip, port: port, ctx: l.ctx, sbx: sbx}, nil
	}
}
