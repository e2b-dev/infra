package ginutils

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type optionalBody struct {
	Memory *bool `json:"memory"`
}

// newTestContext builds a gin.Context whose request reads from body and reports
// contentLength (use -1 to emulate chunked transfer encoding).
func newTestContext(body io.Reader, contentLength int64) *gin.Context {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", body)
	req.ContentLength = contentLength

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	return c
}

func TestParseOptionalBody(t *testing.T) {
	t.Parallel()

	t.Run("chunked request with a body is parsed (ContentLength -1)", func(t *testing.T) {
		t.Parallel()

		// The regression: a chunked client sends a real JSON body but Go reports
		// ContentLength == -1. Gating on ContentLength > 0 would silently drop it.
		c := newTestContext(strings.NewReader(`{"memory":false}`), -1)

		body, err := ParseOptionalBody[optionalBody](t.Context(), c)
		require.NoError(t, err)
		require.NotNil(t, body.Memory)
		assert.False(t, *body.Memory)
	})

	t.Run("body with Content-Length is parsed", func(t *testing.T) {
		t.Parallel()

		raw := `{"memory":false}`
		c := newTestContext(strings.NewReader(raw), int64(len(raw)))

		body, err := ParseOptionalBody[optionalBody](t.Context(), c)
		require.NoError(t, err)
		require.NotNil(t, body.Memory)
		assert.False(t, *body.Memory)
	})

	t.Run("empty body yields the zero value", func(t *testing.T) {
		t.Parallel()

		c := newTestContext(strings.NewReader(""), 0)

		body, err := ParseOptionalBody[optionalBody](t.Context(), c)
		require.NoError(t, err)
		assert.Nil(t, body.Memory, "absent body must default to the zero value")
	})

	t.Run("empty chunked body yields the zero value (ContentLength -1)", func(t *testing.T) {
		t.Parallel()

		// This is the case a naive `ContentLength != 0` switch would break:
		// chunked + no body would attempt to decode an empty stream and 400.
		c := newTestContext(strings.NewReader(""), -1)

		body, err := ParseOptionalBody[optionalBody](t.Context(), c)
		require.NoError(t, err)
		assert.Nil(t, body.Memory)
	})

	t.Run("nil body yields the zero value", func(t *testing.T) {
		t.Parallel()

		c := newTestContext(http.NoBody, 0)
		c.Request.Body = nil

		body, err := ParseOptionalBody[optionalBody](t.Context(), c)
		require.NoError(t, err)
		assert.Nil(t, body.Memory)
	})

	t.Run("malformed body returns an error", func(t *testing.T) {
		t.Parallel()

		c := newTestContext(strings.NewReader(`{"memory":`), -1)

		_, err := ParseOptionalBody[optionalBody](t.Context(), c)
		require.Error(t, err)
	})
}
