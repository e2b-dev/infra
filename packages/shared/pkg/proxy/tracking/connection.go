package tracking

import (
	"net"
	"sync/atomic"
)

type Connection struct {
	net.Conn
	counter *atomic.Int64
}

func NewConnection(conn net.Conn, counter *atomic.Int64) *Connection {
	counter.Add(1)

	return &Connection{
		Conn:    conn,
		counter: counter,
	}
}

func (c *Connection) Close() error {
	err := c.Conn.Close()
	if err != nil {
		return err
	}

	c.counter.Add(-1)

	return nil
}
