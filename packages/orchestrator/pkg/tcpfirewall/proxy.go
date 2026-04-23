package tcpfirewall

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"github.com/inetaf/tcpproxy"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/connlimit"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var _ sandbox.MapSubscriber = (*Proxy)(nil)

var _ network.EgressProxy = (*Proxy)(nil)

type Proxy struct {
	logger       logger.Logger
	sandboxes    *sandbox.Map
	metrics      *Metrics
	limiter      *connlimit.ConnectionLimiter
	featureFlags *featureflags.Client

	// Separate ports for different traffic types to avoid protocol detection blocking
	// on server-first protocols like SSH.
	httpPort  uint16 // For port 80 traffic - HTTP Host header inspection
	tlsPort   uint16 // For port 443 traffic - TLS SNI inspection
	otherPort uint16 // For all other ports - CIDR-only, no protocol inspection

	proxyRules []proxyRule
	proxy      *tcpproxy.Proxy
}

func New(logger logger.Logger, networkConfig network.Config, sandboxes *sandbox.Map, meterProvider metric.MeterProvider, featureFlags *featureflags.Client) *Proxy {
	p := &Proxy{
		httpPort:     networkConfig.SandboxTCPFirewallHTTPPort,
		tlsPort:      networkConfig.SandboxTCPFirewallTLSPort,
		otherPort:    networkConfig.SandboxTCPFirewallOtherPort,
		logger:       logger,
		sandboxes:    sandboxes,
		metrics:      NewMetrics(meterProvider),
		limiter:      connlimit.NewConnectionLimiter(),
		featureFlags: featureFlags,
	}

	p.proxyRules = []proxyRule{
		{dstPort: "80", proxyPort: fmt.Sprintf("%d", p.httpPort), desc: "HTTP"},
		{dstPort: "443", proxyPort: fmt.Sprintf("%d", p.tlsPort), desc: "TLS"},
		{dstPort: "", proxyPort: fmt.Sprintf("%d", p.otherPort), desc: "other TCP"},
	}

	sandboxes.Subscribe(p)

	return p
}

func (p *Proxy) OnInsert(_ context.Context, _ *sandbox.Sandbox) {}

func (p *Proxy) OnNetworkRelease(_ context.Context, sbx *sandbox.Sandbox) {
	p.limiter.Remove(sbx.LifecycleID)
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
	p.proxy.AddHTTPHostMatchRoute(httpAddr, func(_ context.Context, _ string) bool { return true }, newConnectionHandler(ctx, domainHandler, ProtocolHTTP, p.metrics, p.limiter, p.logger, p.sandboxes, p.featureFlags))
	p.proxy.AddRoute(httpAddr, newConnectionHandler(ctx, cidrOnlyHandler, ProtocolHTTP, p.metrics, p.limiter, p.logger, p.sandboxes, p.featureFlags))

	// TLS listener (port 443 traffic): inspect SNI for domain allowlist
	p.proxy.AddSNIMatchRoute(tlsAddr, func(_ context.Context, _ string) bool { return true }, newConnectionHandler(ctx, domainHandler, ProtocolTLS, p.metrics, p.limiter, p.logger, p.sandboxes, p.featureFlags))
	p.proxy.AddRoute(tlsAddr, newConnectionHandler(ctx, cidrOnlyHandler, ProtocolTLS, p.metrics, p.limiter, p.logger, p.sandboxes, p.featureFlags))

	// Other listener (all other ports): CIDR-only check, no protocol inspection
	// This prevents blocking on server-first protocols like SSH
	p.proxy.AddRoute(otherAddr, newConnectionHandler(ctx, cidrOnlyHandler, ProtocolOther, p.metrics, p.limiter, p.logger, p.sandboxes, p.featureFlags))

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

type proxyRule struct {
	dstPort   string // destination port to match (empty = all ports)
	proxyPort string // port to redirect to
	desc      string // description for error messages
}

func (p *Proxy) ruleArgs(s *network.Slot, rule proxyRule) []string {
	args := []string{"-i", s.VethName(), "-p", "tcp"}
	if rule.dstPort != "" {
		args = append(args, "--dport", rule.dstPort)
	}
	args = append(args,
		"-j", "REDIRECT", "--to-port", rule.proxyPort,
	)

	return args
}

func (p *Proxy) OnSlotCreate(s *network.Slot, tables *iptables.IPTables) error {
	for _, rule := range p.proxyRules {
		err := tables.Append("nat", "PREROUTING", p.ruleArgs(s, rule)...)
		if err != nil {
			return fmt.Errorf("error creating redirect rule for %s traffic: %w", rule.desc, err)
		}
	}

	return nil
}

func (p *Proxy) OnSlotDelete(s *network.Slot, tables *iptables.IPTables) error {
	var errs []error
	for _, rule := range p.proxyRules {
		err := tables.Delete("nat", "PREROUTING", p.ruleArgs(s, rule)...)
		if err != nil {
			errs = append(errs, fmt.Errorf("error deleting %s egress proxy redirect rule: %w", rule.desc, err))
		}
	}

	return errors.Join(errs...)
}

func (p *Proxy) Close(_ context.Context) error {
	if p.proxy != nil {
		return p.proxy.Close()
	}

	return nil
}

func (p *Proxy) CABundle() string {
	return ""
}

// handlerFunc is the signature for connection handlers.
type handlerFunc func(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger, metrics *Metrics, protocol Protocol)

var _ tcpproxy.Target = (*connectionHandler)(nil)

// connectionHandler adapts a handler function to tcpproxy.Target interface.
type connectionHandler struct {
	ctx context.Context //nolint:containedctx // base context for request tracing

	handler      handlerFunc
	protocol     Protocol
	metrics      *Metrics
	limiter      *connlimit.ConnectionLimiter
	logger       logger.Logger
	sandboxes    *sandbox.Map
	featureFlags *featureflags.Client
}

func newConnectionHandler(ctx context.Context, handler handlerFunc, protocol Protocol, metrics *Metrics, limiter *connlimit.ConnectionLimiter, logger logger.Logger, sandboxes *sandbox.Map, featureFlags *featureflags.Client) *connectionHandler {
	return &connectionHandler{
		ctx:          ctx,
		handler:      handler,
		protocol:     protocol,
		metrics:      metrics,
		limiter:      limiter,
		logger:       logger,
		sandboxes:    sandboxes,
		featureFlags: featureFlags,
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
		sourceIP, _, _ := net.SplitHostPort(sourceAddr)
		t.logger.Error(ctx, "failed to find sandbox for connection",
			logger.WithSandboxIP(sourceIP),
			zap.Error(err))
		t.metrics.RecordError(ctx, ErrorTypeSandboxLookup, t.protocol)
		conn.Close()

		return
	}

	sandboxID := sbx.Runtime.SandboxID
	// Scope the limiter to this sandbox lifecycle: SandboxID is reused on
	// checkpoint/resume and the IP is reused via the network slot pool, so
	// only LifecycleID is unique per lifecycle.
	limiterKey := sbx.LifecycleID
	sbxLogger := t.logger.With(logger.WithSandboxID(sandboxID))

	// Check per-sandbox connection limit
	maxLimit := t.featureFlags.IntFlag(ctx, featureflags.TCPFirewallMaxConnectionsPerSandbox)
	count, acquired := t.limiter.TryAcquire(limiterKey, maxLimit)
	if !acquired {
		t.metrics.RecordError(ctx, ErrorTypeLimitExceeded, t.protocol)
		conn.Close()

		return
	}

	// Get original destination (before iptables redirect)
	ip, port, err := getOriginalDst(rawConn)
	if err != nil {
		sbxLogger.Error(ctx, "failed to get original destination", zap.Error(err))
		t.metrics.RecordError(ctx, ErrorTypeOrigDst, t.protocol)
		t.limiter.Release(limiterKey)
		conn.Close()

		return
	}

	t.metrics.RecordConnectionsPerSandbox(ctx, count)
	t.metrics.RecordConnection(ctx, t.protocol, sbxHasBYOP(sbx))

	// Wrap the handler to release the connection slot when done
	wrappedHandler := func(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, l logger.Logger, metrics *Metrics, protocol Protocol) {
		defer t.limiter.Release(limiterKey)
		t.handler(ctx, conn, dstIP, dstPort, sbx, l, metrics, protocol)
	}

	wrappedHandler(ctx, conn, ip, port, sbx, sbxLogger, t.metrics, t.protocol)
}
