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
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
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

	// Determine the upstream IP to use
	upstreamIP := dstIP

	// When allowed by domain match, resolve the hostname ourselves and use the resolved IP.
	// This prevents DNS spoofing attacks where the sandbox modifies /etc/hosts to redirect
	// an allowed domain to an arbitrary IP. We ignore the client's destination IP and
	// connect to a legitimate IP that the hostname actually resolves to.
	if matchType == MatchTypeDomain {
		resolvedIP, err := resolveHostnameToPublicIP(ctx, logger, hostname)
		if err != nil {
			logger.Warn(ctx, "Failed to resolve hostname to public IP",
				zap.String("hostname", hostname),
				zap.String("original_dst_ip", dstIP.String()),
				zap.Error(err))
			metrics.RecordError(ctx, ErrorTypeDNSMismatch, protocol)
			conn.Close()

			return
		}

		upstreamIP = resolvedIP
	}

	upstreamAddr := net.JoinHostPort(upstreamIP.String(), fmt.Sprintf("%d", dstPort))
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

// resolveHostnameToPublicIP resolves a hostname and returns the first public (non-internal) IP.
// This prevents DNS spoofing attacks by ignoring the client's destination IP and resolving
// the hostname ourselves. It also prevents DNS rebinding attacks by rejecting hostnames
// that only resolve to internal/private IP addresses.
func resolveHostnameToPublicIP(ctx context.Context, logger logger.Logger, hostname string) (net.IP, error) {
	// Create a context with timeout for DNS lookup
	lookupCtx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()

	// Resolve the hostname to IP addresses using the system resolver (with caching)
	ips, err := dnsResolver.LookupIPAddr(lookupCtx, hostname)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup failed for %q: %w", hostname, err)
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("DNS lookup returned no IPs for %q", hostname)
	}

	// Find the first IP that is NOT in the denied sandbox CIDRs (i.e., a public IP)
	for _, ip := range ips {
		if !isIPInDeniedCIDRs(ip.IP) {
			return ip.IP, nil
		}
	}

	// All resolved IPs are in denied CIDRs (internal/private) - this could be a DNS rebinding attack
	return nil, fmt.Errorf("hostname %q only resolves to internal IPs", hostname)
}

// isIPInDeniedCIDRs checks if an IP is within the denied sandbox CIDRs (internal/private ranges).
func isIPInDeniedCIDRs(ip net.IP) bool {
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
