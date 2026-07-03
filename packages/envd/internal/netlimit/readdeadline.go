package netlimit

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// IdleReadCloser bounds how long a single read from a request body may stall.
// It resets a read deadline before every Read via setDeadline, so a body that
// stops delivering bytes for timeout is failed while a slow-but-progressing
// transfer survives. Use it for bulk uploads only — never for client streams
// (e.g. stdin) where a long quiet gap is normal. timeout <= 0 returns body as-is.
func IdleReadCloser(body io.ReadCloser, setDeadline func(time.Time) error, timeout time.Duration) io.ReadCloser {
	if timeout <= 0 {
		return body
	}

	return &idleReadCloser{ReadCloser: body, setDeadline: setDeadline, timeout: timeout}
}

type idleReadCloser struct {
	io.ReadCloser

	setDeadline func(time.Time) error
	timeout     time.Duration
	unsupported bool
}

func (c *idleReadCloser) Read(p []byte) (int, error) {
	if !c.unsupported {
		switch err := c.setDeadline(time.Now().Add(c.timeout)); {
		case err == nil:
		case errors.Is(err, http.ErrNotSupported):
			// No deadline support behind this writer (e.g. an unwrappable
			// middleware). Stop trying so we never fail a healthy read.
			c.unsupported = true
		default:
			return 0, err
		}
	}

	return c.ReadCloser.Read(p)
}
