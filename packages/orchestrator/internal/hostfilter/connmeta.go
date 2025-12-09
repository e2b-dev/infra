package hostfilter

import (
	"context"
	"net"

	"inet.af/tcpproxy"
)

// connMeta holds per-connection metadata (context for tracing, original destination port)
type connMeta struct {
	net.Conn

	port int
	ctx  context.Context //nolint:containedctx // intentional: carries per-connection context for request tracing
}

func (c *connMeta) OriginalDstPort() int { return c.port }

func (c *connMeta) Context() context.Context { return c.ctx }

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
