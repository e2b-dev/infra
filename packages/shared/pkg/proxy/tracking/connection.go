package tracking

import (
	"net"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Connection struct {
	net.Conn

	counter *atomic.Int64
	key     string

	m *smap.Map[*Connection]
}

func NewConnection(conn net.Conn, counter *atomic.Int64, m *smap.Map[*Connection]) *Connection {
	counter.Add(1)

	c := &Connection{
		Conn:    conn,
		counter: counter,
		m:       m,
	}

	if m != nil {
		c.key = uuid.New().String()

		m.Insert(c.key, c)
	}

	return c
}

func (c *Connection) Reset() error {
	err := c.Conn.(*net.TCPConn).SetLinger(0)
	if err != nil {
		return err
	}

	err = c.Close()
	if err != nil {
		return err
	}

	return nil
}

func (c *Connection) Close() error {
	err := c.Conn.Close()
	if err != nil {
		return err
	}

	c.counter.Add(-1)

	if c.m != nil {
		c.m.Remove(c.key)
	}

	return nil
}
