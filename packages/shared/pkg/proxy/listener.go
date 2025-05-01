package proxy

import (
	"net"
	"sync/atomic"

	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
)

type trackedListener struct {
	net.Listener
	counter *atomic.Int64
}

func newTrackedListener(l net.Listener, counter *atomic.Int64) *trackedListener {
	return &trackedListener{
		Listener: l,
		counter:  counter,
	}
}

func (l *trackedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return pool.NewTrackedConnection(conn, l.counter), nil
}
