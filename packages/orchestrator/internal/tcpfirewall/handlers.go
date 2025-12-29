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

	// dnsLookupTimeout is the maximum time to wait for DNS resolution.
	dnsLookupTimeout = 5 * time.Second

	noHostnameValue = ""
)

// dnsResolver uses CGO's getaddrinfo which respects the system's DNS cache
// (e.g., systemd-resolved, nscd). This provides caching and uses the host's
// configured DNS infrastructure rather than Go's pure resolver.
var dnsResolver = &net.Resolver{
	PreferGo: false,
}

// domainHandler handles connections with hostname information (HTTP Host header or TLS SNI).
func domainHandler(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger, metrics *Metrics, protocol Protocol) {
	// Get hostname from tcpproxy's wrapped connection
	// Hostname can be empty, this is the case e.g. for https://1.1.1.1 like requests
	var hostname string
	if tc, ok := conn.(*tcpproxy.Conn); ok {
		hostname = tc.HostName
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

	// When allowed by domain match, verify hostname resolves to dstIP to prevent DNS spoofing attacks.
	// A malicious client could claim a hostname that doesn't resolve to the IP they're connecting to.
	// Once verified, we use the dstIP directly (avoids another DNS lookup for the upstream connection).
	if matchType == MatchTypeDomain {
		if !verifyHostnameResolvesToIP(ctx, logger, hostname, dstIP) {
			logger.Warn(ctx, "Hostname does not resolve to the same destination IP",
				zap.String("hostname", hostname),
				zap.String("dst_ip", dstIP.String()))
			metrics.RecordError(ctx, ErrorTypeDNSMismatch, protocol)
			conn.Close()

			return
		}
	}

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

// verifyHostnameResolvesToIP checks if the hostname resolves to the given IP address.
// This prevents DNS spoofing attacks where a client claims a hostname that doesn't
// actually resolve to the IP they're connecting to.
func verifyHostnameResolvesToIP(ctx context.Context, logger logger.Logger, hostname string, expectedIP net.IP) bool {
	// Create a context with timeout for DNS lookup
	lookupCtx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()

	// Resolve the hostname to IP addresses using the system resolver (with caching)
	ips, err := dnsResolver.LookupIPAddr(lookupCtx, hostname)
	if err != nil {
		// DNS lookup failed - could be network issue or non-existent domain
		logger.Warn(ctx, "DNS lookup failed", zap.Error(err), zap.String("hostname", hostname), zap.String("expected_ip", expectedIP.String()))

		return false
	}

	// Check if any resolved IP matches the expected IP
	for _, ip := range ips {
		if ip.IP.Equal(expectedIP) {
			return true
		}
	}

	return false
}
