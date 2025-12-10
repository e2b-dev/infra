package tcpfirewall

import (
	"context"
	"net"

	"inet.af/tcpproxy"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// connMeta holds per-connection metadata (context for tracing, original destination port)
type connMeta struct {
	net.Conn

	ip     net.IP
	port   int
	ctx    context.Context //nolint:containedctx // intentional: carries per-connection context for request tracing
	sbx    *sandbox.Sandbox
	logger logger.Logger
}

// unwrapConnMeta extracts our connection metadata from a (possibly wrapped) connection.
func unwrapConnMeta(conn net.Conn) (*connMeta, bool) {
	for c := conn; c != nil; {
		if cm, ok := c.(*connMeta); ok {
			return cm, true
		}

		// Try to unwrap - tcpproxy.Conn has Conn as a public field
		if tc, ok := c.(*tcpproxy.Conn); ok {
			c = tc.Conn
		} else {
			break
		}
	}

	return nil, false
}
