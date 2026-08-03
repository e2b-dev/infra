package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestSizeLimiter(t *testing.T) {
	t.Parallel()

	const limit = 16

	tests := []struct {
		name        string
		body        string
		wantErr     bool
		wantRead    string
		wantStatus  int
		wantConnHdr string
	}{
		{
			name:       "body within the limit is read in full",
			body:       strings.Repeat("a", limit),
			wantRead:   strings.Repeat("a", limit),
			wantStatus: http.StatusOK,
		},
		{
			name:        "body over the limit fails the read",
			body:        strings.Repeat("a", limit+1),
			wantErr:     true,
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantConnHdr: "close",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				read    []byte
				readErr error
			)

			r := gin.New()
			r.Use(RequestSizeLimiter(limit))
			r.POST("/", func(c *gin.Context) {
				read, readErr = io.ReadAll(c.Request.Body)
				require.NoError(t, c.Request.Body.Close())

				if readErr == nil {
					c.Status(http.StatusOK)
				}
			})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(tt.body))
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)

			if tt.wantErr {
				var tooLarge *http.MaxBytesError
				require.ErrorAs(t, readErr, &tooLarge)
			} else {
				require.NoError(t, readErr)
				assert.Equal(t, tt.wantRead, string(read))
			}

			assert.Equal(t, tt.wantStatus, rr.Code)
			assert.Equal(t, tt.wantConnHdr, rr.Header().Get("Connection"))
			// The limiter must never write a body; the error handler is the sole writer.
			assert.Empty(t, rr.Body.String())
		})
	}
}
