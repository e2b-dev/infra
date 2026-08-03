package middleware

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequestSizeLimiter caps how much of a request body the server will read.
//
// On overflow it records the 413 status without writing a body: the OpenAPI
// validator's error handler is the sole writer of the documented error
// envelope, and a body committed here would corrupt it.
func RequestSizeLimiter(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = &limitedBody{
			ctx:  c,
			body: http.MaxBytesReader(c.Writer, c.Request.Body, limit),
		}

		c.Next()
	}
}

type limitedBody struct {
	ctx  *gin.Context
	body io.ReadCloser
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		// The rest of the body is never read, so the connection cannot be reused.
		b.ctx.Header("Connection", "close")
		b.ctx.Status(http.StatusRequestEntityTooLarge)
	}

	return n, err
}

func (b *limitedBody) Close() error {
	return b.body.Close()
}
