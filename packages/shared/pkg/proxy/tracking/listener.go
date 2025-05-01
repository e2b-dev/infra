package tracking

import (
	"net"
	"sync/atomic"
)

type Listener struct {
	net.Listener
	counter *atomic.Int64
}

func NewListener(l net.Listener, counter *atomic.Int64) *Listener {
	return &Listener{
		Listener: l,
		counter:  counter,
	}
}

func (l *Listener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return NewConnection(conn, l.counter), nil
}
