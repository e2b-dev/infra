package pool

import (
	"net"
	"sync/atomic"
)

type trackedConnection struct {
	net.Conn
	counter *atomic.Int64
}

func newTrackedConnection(conn net.Conn, counter *atomic.Int64) *trackedConnection {
	counter.Add(1)

	return &trackedConnection{
		Conn:    conn,
		counter: counter,
	}
}

func (c *trackedConnection) Close() error {
	c.counter.Add(-1)

	return c.Conn.Close()
}
