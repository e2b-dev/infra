package tcpfirewall

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/inetaf/tcpproxy"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Proxy struct {
	logger    logger.Logger
	sandboxes *sandbox.Map
	metrics   *Metrics

	// Separate ports for different traffic types to avoid protocol detection blocking
	// on server-first protocols like SSH.
	httpPort  uint16 // For port 80 traffic - HTTP Host header inspection
	tlsPort   uint16 // For port 443 traffic - TLS SNI inspection
	otherPort uint16 // For all other ports - CIDR-only, no protocol inspection

	proxy *tcpproxy.Proxy
}

func New(logger logger.Logger, networkConfig network.Config, sandboxes *sandbox.Map, meterProvider metric.MeterProvider) *Proxy {
	return &Proxy{
		httpPort:  networkConfig.SandboxTCPFirewallHTTPPort,
		tlsPort:   networkConfig.SandboxTCPFirewallTLSPort,
		otherPort: networkConfig.SandboxTCPFirewallOtherPort,
		logger:    logger,
		sandboxes: sandboxes,
		metrics:   NewMetrics(meterProvider),
	}
}

func (p *Proxy) Start(ctx context.Context) error {
	p.proxy = &tcpproxy.Proxy{}

	p.proxy.ListenFunc = func(network, laddr string) (net.Listener, error) {
		lc := net.ListenConfig{}
		ln, err := lc.Listen(ctx, network, laddr)
		if err != nil {
			return nil, err
		}

		return &resilientListener{
			Listener: ln,
			ctx:      ctx,
			logger:   p.logger,
		}, nil
	}

	// Three separate addresses for different traffic types.
	// iptables redirects traffic based on original destination port:
	// - dport 80 → httpAddr (HTTP Host header inspection)
	// - dport 443 → tlsAddr (TLS SNI inspection)
	// - other dports → otherAddr (CIDR-only, no protocol inspection)
	httpAddr := fmt.Sprintf("0.0.0.0:%d", p.httpPort)
	tlsAddr := fmt.Sprintf("0.0.0.0:%d", p.tlsPort)
	otherAddr := fmt.Sprintf("0.0.0.0:%d", p.otherPort)

	// HTTP listener (port 80 traffic): inspect Host header for domain allowlist
	p.proxy.AddHTTPHostMatchRoute(httpAddr, func(_ context.Context, _ string) bool { return true }, newConnectionHandler(ctx, domainHandler, ProtocolHTTP, p.metrics, p.logger, p.sandboxes))
	p.proxy.AddRoute(httpAddr, newConnectionHandler(ctx, cidrOnlyHandler, ProtocolHTTP, p.metrics, p.logger, p.sandboxes))

	// TLS listener (port 443 traffic): inspect SNI for domain allowlist
	p.proxy.AddSNIMatchRoute(tlsAddr, func(_ context.Context, _ string) bool { return true }, newConnectionHandler(ctx, domainHandler, ProtocolTLS, p.metrics, p.logger, p.sandboxes))
	p.proxy.AddRoute(tlsAddr, newConnectionHandler(ctx, cidrOnlyHandler, ProtocolTLS, p.metrics, p.logger, p.sandboxes))

	// Other listener (all other ports): CIDR-only check, no protocol inspection
	// This prevents blocking on server-first protocols like SSH
	p.proxy.AddRoute(otherAddr, newConnectionHandler(ctx, cidrOnlyHandler, ProtocolOther, p.metrics, p.logger, p.sandboxes))

	p.logger.Info(ctx, "TCP firewall proxy started",
		zap.Uint16("http_port", p.httpPort),
		zap.Uint16("tls_port", p.tlsPort),
		zap.Uint16("other_port", p.otherPort))

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
	ctx context.Context //nolint:containedctx // base context for request tracing

	handler   handlerFunc
	protocol  Protocol
	metrics   *Metrics
	logger    logger.Logger
	sandboxes *sandbox.Map
}

func newConnectionHandler(ctx context.Context, handler handlerFunc, protocol Protocol, metrics *Metrics, logger logger.Logger, sandboxes *sandbox.Map) *connectionHandler {
	return &connectionHandler{
		ctx:       ctx,
		handler:   handler,
		protocol:  protocol,
		metrics:   metrics,
		logger:    logger,
		sandboxes: sandboxes,
	}
}

func (t *connectionHandler) HandleConn(conn net.Conn) {
	// Request tracing context.
	ctx := t.ctx

	// Get the underlying connection for sandbox lookup and original dst.
	// tcpproxy may wrap in *tcpproxy.Conn for peeked bytes.
	rawConn := tcpproxy.UnderlyingConn(conn)

	// Look up sandbox by source address
	sourceAddr := rawConn.RemoteAddr().String()
	sbx, err := t.sandboxes.GetByHostPort(sourceAddr)
	if err != nil {
		t.logger.Error(ctx, "failed to find sandbox for connection", zap.String("source", sourceAddr), zap.Error(err))
		t.metrics.RecordError(ctx, ErrorTypeSandboxLookup, t.protocol)
		conn.Close()

		return
	}

	// Get original destination (before iptables redirect)
	ip, port, err := getOriginalDst(rawConn)
	if err != nil {
		t.logger.Error(ctx, "failed to get original destination", zap.Error(err))
		t.metrics.RecordError(ctx, ErrorTypeOrigDst, t.protocol)
		conn.Close()

		return
	}

	logger := t.logger.With(logger.WithSandboxID(sbx.Runtime.SandboxID))
	t.metrics.RecordConnection(ctx, t.protocol)

	t.handler(ctx, conn, ip, port, sbx, logger, t.metrics, t.protocol)
}
