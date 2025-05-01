package pool

import (
	"net"
	"sync/atomic"
)

type TrackedConnection struct {
	net.Conn
	counter *atomic.Int64
}

func NewTrackedConnection(conn net.Conn, counter *atomic.Int64) *TrackedConnection {
	counter.Add(1)

	return &TrackedConnection{
		Conn:    conn,
		counter: counter,
	}
}

func (c *TrackedConnection) Close() error {
	c.counter.Add(-1)

	return c.Conn.Close()
}
