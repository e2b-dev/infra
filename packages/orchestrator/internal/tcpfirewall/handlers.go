package tcpfirewall

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/inetaf/tcpproxy"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

const (
	// upstreamDialTimeout is the maximum time to wait for upstream connections.
	// This prevents goroutine leaks from slow/unresponsive DNS or connections.
	upstreamDialTimeout = 30 * time.Second

)

// domainHandler handles connections with hostname information (HTTP Host header or TLS SNI).
func domainHandler(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger, metrics *Metrics, protocol Protocol) {
	// Get hostname from tcpproxy's wrapped connection (HTTP Host or TLS SNI).
	// Hostname can be empty, e.g. for https://1.1.1.1 like requests.
	var hostname string
	if tc, ok := conn.(*tcpproxy.Conn); ok {
		hostname = tc.HostName
	}

	allowed, matchType, err := isEgressAllowed(sbx, hostname, dstIP, uint16(dstPort))
	if err != nil {
		logger.Error(ctx, "Egress check failed", zap.Error(err))
		metrics.RecordError(ctx, ErrorTypeEgressCheck, protocol)
		conn.Close()

		return
	}

	if !allowed {
		metrics.RecordDecision(ctx, DecisionBlocked, protocol, matchType)
		conn.Close()

		return
	}

	metrics.RecordDecision(ctx, DecisionAllowed, protocol, matchType)

	// When allowed by domain match, dial the hostname directly (not the sandbox's resolved IP).
	// This prevents DNS spoofing attacks where the sandbox modifies /etc/hosts to redirect
	// an allowed domain to an arbitrary IP. We use Go's net.Dialer which provides built-in
	// Happy Eyeballs (RFC 8305) for multi-IP fallback when some IPs are unreachable.
	// After connecting, we verify the connected IP is not internal/private.
	if matchType == MatchTypeDomain {
		upstreamAddr := net.JoinHostPort(hostname, fmt.Sprintf("%d", dstPort))
		proxyWithIPVerification(ctx, conn, upstreamAddr, logger, metrics, protocol)

		return
	}

	// For non-domain matches, use the original destination IP
	upstreamAddr := net.JoinHostPort(dstIP.String(), fmt.Sprintf("%d", dstPort))
	proxy(ctx, conn, upstreamAddr, metrics, protocol)
}

// cidrOnlyHandler handles connections without hostname information.
func cidrOnlyHandler(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger, metrics *Metrics, protocol Protocol) {
	// No hostname available for CIDR-only handler
	allowed, matchType, err := isEgressAllowed(sbx, "", dstIP, uint16(dstPort))
	if err != nil {
		logger.Error(ctx, "Egress check failed", zap.Error(err))
		metrics.RecordError(ctx, ErrorTypeEgressCheck, protocol)
		conn.Close()

		return
	}

	if !allowed {
		metrics.RecordDecision(ctx, DecisionBlocked, protocol, matchType)
		conn.Close()

		return
	}

	metrics.RecordDecision(ctx, DecisionAllowed, protocol, matchType)

	upstreamAddr := net.JoinHostPort(dstIP.String(), fmt.Sprintf("%d", dstPort))

	proxy(ctx, conn, upstreamAddr, metrics, protocol)
}

// proxy proxies the connection to the upstream address.
func proxy(ctx context.Context, conn net.Conn, upstreamAddr string, metrics *Metrics, protocol Protocol) {
	tracker := metrics.TrackConnection(protocol)
	defer tracker.Close(ctx)

	dp := &tcpproxy.DialProxy{
		Addr:        upstreamAddr,
		DialTimeout: upstreamDialTimeout,
	}
	dp.HandleConn(conn)
}

// proxyWithIPVerification dials the upstream hostname using Go's net.Dialer (which provides
// built-in Happy Eyeballs / RFC 8305 for multi-IP fallback), and verifies the resolved IP
// is not internal/private BEFORE connecting. This prevents DNS rebinding attacks while
// preserving multi-IP reliability.
//
// The ControlContext callback is called after DNS resolution but before the TCP connect()
// syscall, so no TCP handshake occurs to internal IPs.
func proxyWithIPVerification(ctx context.Context, conn net.Conn, upstreamAddr string, logger logger.Logger, metrics *Metrics, protocol Protocol) {
	tracker := metrics.TrackConnection(protocol)
	defer tracker.Close(ctx)

	// Use tcpproxy.DialProxy with a custom DialContext that verifies resolved IPs
	dp := &tcpproxy.DialProxy{
		Addr:        upstreamAddr,
		DialTimeout: upstreamDialTimeout,
		DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			// Use Go's net.Dialer which has built-in Happy Eyeballs (RFC 8305)
			// This automatically tries multiple IPs with proper fallback
			dialer := &net.Dialer{
				Timeout: upstreamDialTimeout,
				// ControlContext is called after DNS resolution but BEFORE the TCP connect() syscall.
				// The 'address' parameter contains the resolved IP:port, allowing us to block
				// connections to internal IPs before any TCP handshake occurs.
				ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
					host, _, err := net.SplitHostPort(address)
					if err != nil {
						return fmt.Errorf("failed to parse resolved address %q: %w", address, err)
					}

					resolvedIP := net.ParseIP(host)
					if resolvedIP == nil {
						return fmt.Errorf("failed to parse IP from resolved address %q", host)
					}

					if isIPInAlwaysDeniedCIDRs(resolvedIP) {
						logger.Warn(ctx, "Blocked connection to internal IP via hostname",
							zap.String("upstream_addr", addr),
							zap.String("resolved_ip", resolvedIP.String()))
						metrics.RecordError(ctx, ErrorTypeResolvedIPBlocked, protocol)

						return fmt.Errorf("hostname resolved to internal IP %s", resolvedIP)
					}

					return nil
				},
			}

			return dialer.DialContext(dialCtx, network, addr)
		},
	}
	dp.HandleConn(conn)
}

// isEgressAllowed checks if egress is allowed based on domain, CIDR, and port rules.
// Returns the allowed status and the match type for metrics.
// Priority order:
//  1. Allow entries (if domain or CIDR matches host AND port → allow)
//  2. Deny entries (if CIDR matches host AND port → deny)
//  3. Default: allow
func isEgressAllowed(sbx *sandbox.Sandbox, hostname string, toIP net.IP, toPort uint16) (bool, MatchType, error) {
	egress := sbx.Config.GetNetworkEgress()
	if egress == nil {
		// No egress configuration, allow all traffic.
		return true, MatchTypeNone, nil
	}

	// Priority 1: Check allowed entries (domains and CIDRs unified)
	for _, entry := range egress.GetAllowed() {
		matched, matchType, err := matchRule(entry, hostname, toIP, toPort, true)
		if err != nil {
			return false, MatchTypeNone, fmt.Errorf("invalid allowed entry %q: %w", entry, err)
		}

		if matched {
			return true, matchType, nil
		}
	}

	// Priority 2: Check denied entries
	for _, entry := range egress.GetDenied() {
		matched, matchType, err := matchRule(entry, hostname, toIP, toPort, false)
		if err != nil {
			return false, MatchTypeNone, fmt.Errorf("invalid denied entry %q: %w", entry, err)
		}

		if matched {
			return false, matchType, nil
		}
	}

	// Default: allow all traffic.
	return true, MatchTypeNone, nil
}

// matchRule checks if a single entry matches the given hostname, IP, and port.
// When checkDomains is true, domain entries are evaluated; otherwise they are skipped.
func matchRule(entry, hostname string, ip net.IP, port uint16, checkDomains bool) (bool, MatchType, error) {
	rule, err := sandbox_network.ParseRule(entry)
	if err != nil {
		return false, MatchTypeNone, err
	}

	if rule.IsDomain {
		if checkDomains && hostname != "" && matchDomain(hostname, rule.Host) && rule.PortInRange(port) {
			return true, MatchTypeDomain, nil
		}

		return false, MatchTypeNone, nil
	}

	cidr := sandbox_network.AddressStringToCIDR(rule.Host)
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, MatchTypeNone, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}

	if ipNet.Contains(ip) && rule.PortInRange(port) {
		return true, MatchTypeCIDR, nil
	}

	return false, MatchTypeNone, nil
}

// matchDomain checks if a hostname matches a domain pattern.
// Patterns can be exact matches, wildcards (*), or suffix wildcards (*.example.com).
func matchDomain(hostname, pattern string) bool {
	switch {
	case pattern == "":
		// Empty pattern should never match
		return false
	case strings.EqualFold(pattern, hostname):
		return true
	case strings.EqualFold(pattern, "*"):
		return true
	case strings.HasPrefix(pattern, "*."):
		suffix := pattern[1:]
		if strings.HasSuffix(strings.ToLower(hostname), strings.ToLower(suffix)) {
			return true
		}
	}

	return false
}

// isIPInAlwaysDeniedCIDRs checks if an IP is within the denied sandbox CIDRs (internal/private ranges).
func isIPInAlwaysDeniedCIDRs(ip net.IP) bool {
	for _, cidr := range sandbox_network.DeniedSandboxCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}

		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}
