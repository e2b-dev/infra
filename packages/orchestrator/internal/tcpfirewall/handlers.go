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

func domainHandler(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger) {
	sourceAddr := conn.RemoteAddr().String()

	// Get hostname from tcpproxy's wrapped connection
	var hostname string
	if tc, ok := conn.(*tcpproxy.Conn); ok {
		hostname = tc.HostName
	}

	if hostname == "" {
		// No hostname found, this is the case e.g. for https://1.1.1.1 like requests
		logger.Debug(ctx, "No hostname found, ignoring hostname based filter", zap.String("source_addr", sourceAddr))
	}

	allowed, err := isEgressAllowed(sbx, hostname, dstIP)
	if err != nil {
		logger.Error(ctx, "Egress check failed", zap.Error(err))
		conn.Close()

		return
	}

	if !allowed {
		logger.Debug(ctx, "Denied sandbox egress connection",
			zap.String("hostname", hostname),
			zap.String("destination_ip", dstIP.String()),
			zap.String("source_addr", sourceAddr),
		)
		conn.Close()

		return
	}

	// Proxy to IP directly (hostname already resolved by the client)
	upstreamAddr := net.JoinHostPort(dstIP.String(), fmt.Sprintf("%d", dstPort))

	logger.Debug(ctx, "Proxying sandbox egress hostname filter connection",
		zap.String("hostname", hostname),
		zap.String("source_addr", sourceAddr),
		zap.String("upstream_addr", upstreamAddr),
	)

	proxy(conn, upstreamAddr)
}

func cidrOnlyHandler(ctx context.Context, conn net.Conn, dstIP net.IP, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger) {
	sourceAddr := conn.RemoteAddr().String()

	// No hostname available for CIDR-only handler
	allowed, err := isEgressAllowed(sbx, noHostnameValue, dstIP)
	if err != nil {
		logger.Error(ctx, "Egress check failed", zap.Error(err))
		conn.Close()

		return
	}

	if !allowed {
		logger.Debug(ctx, "Denied sandbox egress connection",
			zap.String("destination_ip", dstIP.String()),
			zap.String("source_addr", sourceAddr),
		)
		conn.Close()

		return
	}

	upstreamAddr := net.JoinHostPort(dstIP.String(), fmt.Sprintf("%d", dstPort))

	logger.Debug(ctx, "Proxying sandbox egress IP filter connection",
		zap.String("source_addr", sourceAddr),
		zap.String("upstream_addr", upstreamAddr),
	)

	proxy(conn, upstreamAddr)
}

// proxy proxies the connection to the upstream address.
func proxy(conn net.Conn, upstreamAddr string) {
	dp := &tcpproxy.DialProxy{
		Addr:        upstreamAddr,
		DialTimeout: upstreamDialTimeout,
	}
	dp.HandleConn(conn)
}

// isEgressAllowed checks if egress is allowed based on domain and CIDR rules.
// Priority order:
//  1. Allow domain / Allow CIDR (if either matches → allow)
//  2. Deny domain / Deny CIDR (if either matches → deny)
//  3. Default: allow
func isEgressAllowed(sbx *sandbox.Sandbox, hostname string, ip net.IP) (bool, error) {
	if networkConfig := sbx.Config.Network; networkConfig != nil {
		egress := networkConfig.GetEgress()

		// Priority 1: Check allowed domains
		if hostname != noHostnameValue {
			for _, domain := range egress.GetAllowedDomains() {
				if matchDomain(hostname, domain) {
					return true, nil // Explicitly allowed by domain
				}
			}
		}

		// Priority 1: Check allowed CIDRs
		for _, cidr := range egress.GetAllowedCidrs() {
			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return false, fmt.Errorf("invalid allowed CIDR %q: %w", cidr, err)
			}

			if ipNet.Contains(ip) {
				return true, nil // Explicitly allowed by CIDR
			}
		}

		// Priority 2: Check denied CIDRs
		for _, cidr := range egress.GetDeniedCidrs() {
			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return false, fmt.Errorf("invalid denied CIDR %q: %w", cidr, err)
			}

			if ipNet.Contains(ip) {
				return false, nil // Blocked by CIDR
			}
		}
	}

	// Default: allow
	return true, nil
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
