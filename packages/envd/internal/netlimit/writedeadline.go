// Package netlimit bounds how long a single write to a client may stall.
// envd serves long-lived streams over HTTP/1.1; if a client stops reading, TCP
// backpressure blocks the write — and the process/stream behind it — forever.
// Setting a fresh deadline before every write fires only on a stuck write, never
// a healthy idle stream (http.Server.WriteTimeout can't: it's an absolute deadline).
package netlimit

import (
	"net"
	"time"
)

// WriteTimeoutListener wraps a net.Listener so every accepted connection
// enforces a per-write idle timeout. A timeout <= 0 disables the behavior and
// returns the underlying listener unchanged (a built-in kill switch).
func WriteTimeoutListener(inner net.Listener, timeout time.Duration) net.Listener {
	if timeout <= 0 {
		return inner
	}

	return &writeTimeoutListener{Listener: inner, timeout: timeout}
}

type writeTimeoutListener struct {
	net.Listener

	timeout time.Duration
}

func (l *writeTimeoutListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return &writeTimeoutConn{Conn: conn, timeout: l.timeout}, nil
}

// writeTimeoutConn resets the write deadline before each write. We deliberately
// do NOT implement io.ReaderFrom: a single sendfile deadline would span a whole
// download and kill a legitimate large-but-progressing transfer. The buffered
// copy fallback gives each chunk its own fresh deadline, so only a genuinely
// stalled chunk trips the timeout.
type writeTimeoutConn struct {
	net.Conn

	timeout time.Duration
}

func (c *writeTimeoutConn) Write(b []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}

	return c.Conn.Write(b)
}
