package legacy

import (
	"bytes"
	"connectrpc.com/connect"
	spec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem/filesystemconnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInterceptor(t *testing.T) {
	t.Run("unary interceptor converts when necessary", func(t *testing.T) {
		// setup
		mockFS := NewMockFilesystemHandler(t)
		_, handler := spec.NewFilesystemHandler(
			mockFS, connect.WithInterceptors(Convert()),
		)
		reqBody := bytes.NewBufferString("{}")
		req := httptest.NewRequest("POST", spec.FilesystemWatchDirProcedure, reqBody)
		req.Header.Set("Content-Type", "application/grpc+json")
		w := httptest.NewRecorder()

		// run the test
		handler.ServeHTTP(w, req)

		// verify results
		resp := w.Result()
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
		assert.Equal(t, "foo", string(body))
	})
}
