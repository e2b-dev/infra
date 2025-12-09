package hostfilter

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"go.uber.org/zap"
	"inet.af/tcpproxy"
)

const (
	// upstreamDialTimeout is the maximum time to wait for upstream connections.
	// This prevents goroutine leaks from slow/unresponsive DNS or connections.
	upstreamDialTimeout = 30 * time.Second
)

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
