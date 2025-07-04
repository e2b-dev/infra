package legacy

import (
	"bytes"
	"connectrpc.com/connect"
	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem/filesystemconnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"io"
	"net/http/httptest"
	"testing"
)

func TestFilesystemClient_FieldFormatter(t *testing.T) {
	fsh := NewMockFilesystemHandler(t)
	fsh.EXPECT().Move(mock.Anything, mock.Anything).Return(connect.NewResponse(&filesystem.MoveResponse{
		Entry: &filesystem.EntryInfo{
			Name: "test name",
		},
		Testing: true,
	}), nil)

	_, handler := filesystemconnect.NewFilesystemHandler(fsh,
		connect.WithInterceptors(
			Convert(),
		),
	)

	t.Run("can return all fields", func(t *testing.T) {
		buf := bytes.NewBuffer([]byte(`{}`))
		req := httptest.NewRequest("POST", filesystemconnect.FilesystemMoveProcedure, buf)
		req.Header.Set("content-type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)

		data, err := io.ReadAll(w.Body)
		require.NoError(t, err)
		assert.Equal(t, `{"entry":{"name":"test name"}, "testing":true}`, string(data))
	})

	t.Run("can hide fields when appropriate", func(t *testing.T) {
		buf := bytes.NewBuffer([]byte(`{}`))
		req := httptest.NewRequest("POST", filesystemconnect.FilesystemMoveProcedure, buf)
		req.Header.Set("user-agent", "connect-python")
		req.Header.Set("content-type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)

		data, err := io.ReadAll(w.Body)
		require.NoError(t, err)
		assert.Equal(t, string(data), `{"entry":{"name":"test name"}}`)
	})
}
