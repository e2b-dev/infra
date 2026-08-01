package middleware

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequestSizeLimiter caps how much of a request body the server will read.
//
// It deliberately writes no response of its own: it only records the 413 status
// on the context and lets the OpenAPI validator's error handler render the
// documented {code, message} error body. Writing here would commit a plain-text
// body that the error handler then appends JSON to, leaving clients with a
// response they cannot parse.
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
		// Only records the status, nothing is written to the client yet.
		b.ctx.Status(http.StatusRequestEntityTooLarge)
	}

	return n, err
}

func (b *limitedBody) Close() error {
	return b.body.Close()
}
