package tcpfirewall

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"go.uber.org/zap"
	"inet.af/tcpproxy"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	// upstreamDialTimeout is the maximum time to wait for upstream connections.
	// This prevents goroutine leaks from slow/unresponsive DNS or connections.
	upstreamDialTimeout = 30 * time.Second

	noHostnameValue = ""
)

// domainHandler handles connections with hostname information (HTTP Host header or TLS SNI).
func domainHandler(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger, metrics *Metrics, protocol Protocol) {
	// Get hostname from tcpproxy's wrapped connection
	var hostname string
	if tc, ok := conn.(*tcpproxy.Conn); ok {
		hostname = tc.HostName
	}

	if hostname == noHostnameValue {
		// No hostname found, this is the case e.g. for https://1.1.1.1 like requests
		logger.Debug(ctx, "No hostname found, ignoring hostname based filter")
	}

	allowed, matchType, err := isEgressAllowed(sbx, hostname, dstIP)
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

	// Proxy to IP directly (hostname already resolved by the client)
	upstreamAddr := net.JoinHostPort(dstIP.String(), fmt.Sprintf("%d", dstPort))

	proxy(ctx, conn, upstreamAddr, metrics, protocol)
}

// cidrOnlyHandler handles connections without hostname information.
func cidrOnlyHandler(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger, metrics *Metrics, protocol Protocol) {
	// No hostname available for CIDR-only handler
	allowed, matchType, err := isEgressAllowed(sbx, noHostnameValue, dstIP)
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

// isEgressAllowed checks if egress is allowed based on domain and CIDR rules.
// Returns the allowed status and the match type for metrics.
// Priority order:
//  1. Allow domain / Allow CIDR (if either matches → allow)
//  2. Deny domain / Deny CIDR (if either matches → deny)
//  3. Default: allow
func isEgressAllowed(sbx *sandbox.Sandbox, hostname string, ip net.IP) (bool, MatchType, error) {
	networkConfig := sbx.Config.Network
	if networkConfig == nil {
		// No network configuration, allow all traffic.
		return true, MatchTypeNone, nil
	}

	egress := networkConfig.GetEgress()
	if egress == nil {
		// No egress configuration, allow all traffic.
		return true, MatchTypeNone, nil
	}

	// Priority 1: Check allowed domains
	if hostname != noHostnameValue {
		for _, domain := range egress.GetAllowedDomains() {
			if matchDomain(hostname, domain) {
				return true, MatchTypeDomain, nil // Explicitly allowed by domain
			}
		}
	}

	// Priority 1: Check allowed CIDRs
	for _, cidr := range egress.GetAllowedCidrs() {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return false, MatchTypeNone, fmt.Errorf("invalid allowed CIDR %q: %w", cidr, err)
		}

		if ipNet.Contains(ip) {
			return true, MatchTypeCIDR, nil // Explicitly allowed by CIDR
		}
	}

	// Priority 2: Check denied CIDRs
	for _, cidr := range egress.GetDeniedCidrs() {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return false, MatchTypeNone, fmt.Errorf("invalid denied CIDR %q: %w", cidr, err)
		}

		if ipNet.Contains(ip) {
			return false, MatchTypeCIDR, nil // Blocked by CIDR
		}
	}

	// Default: allow all traffic.
	return true, MatchTypeNone, nil
}

// matchDomain checks if a hostname matches a domain pattern.
// Patterns can be exact matches, wildcards (*), or suffix wildcards (*.example.com).
func matchDomain(hostname, pattern string) bool {
	switch {
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
