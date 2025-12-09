package hostfilter

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
)

func allowHandler(ctx context.Context, conn net.Conn, dstPort int, sbx *sandbox.Sandbox, logger logger.Logger) {
	sourceAddr := conn.RemoteAddr().String()

	// Get hostname from tcpproxy's wrapped connection
	var hostname string
	if tc, ok := conn.(*tcpproxy.Conn); ok {
		hostname = tc.HostName
	}

	if hostname == "" {
		logger.Debug(ctx, "No hostname found, denying sandbox egress hostname filter connection", zap.String("source_addr", sourceAddr))
		conn.Close()

		return
	}

	allowed, err := isAllowed(sbx, hostname)
	if err != nil {
		logger.Error(ctx, "Allowed sandbox egress hostname filter check failed", zap.Error(err))
		conn.Close()

		return
	}

	if !allowed {
		logger.Debug(ctx, "Denied sandbox egress hostname filter connection",
			zap.String("hostname", hostname),
			zap.String("source_addr", sourceAddr),
		)
		conn.Close()

		return
	}

	upstreamAddr := net.JoinHostPort(hostname, fmt.Sprintf("%d", dstPort))

	logger.Debug(ctx, "Proxying sandbox egress hostname filter connection",
		zap.String("source_addr", sourceAddr),
		zap.String("upstream_addr", upstreamAddr),
	)

	dp := &tcpproxy.DialProxy{
		Addr:        upstreamAddr,
		DialTimeout: upstreamDialTimeout,
	}
	dp.HandleConn(conn)
}

func denyHandler(ctx context.Context, conn net.Conn, _ int, _ *sandbox.Sandbox, logger logger.Logger) {
	conn.Close()
	remoteAddr := conn.RemoteAddr().String()

	logger.Debug(ctx, "Denied sandbox egress hostname filter connection",
		zap.String("remote_addr", remoteAddr),
	)
}

func isAllowed(sbx *sandbox.Sandbox, hostname string) (bool, error) {
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
