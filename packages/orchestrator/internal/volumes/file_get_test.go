package volumes

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type mockGetFileServer struct {
	grpc.ServerStream
	mock.Mock
}

func (m *mockGetFileServer) Send(resp *orchestrator.VolumeFileGetResponse) error {
	args := m.Called(resp)
	return args.Error(0)
}

func (m *mockGetFileServer) Context() context.Context {
	args := m.Called()
	return args.Get(0).(context.Context)
}

func TestFileGet(t *testing.T) {
	s, tmpdir, volumeInfo := setupTestService(t)

	t.Run("get file", func(t *testing.T) {
		filename := "test-get.txt"
		content := []byte("hello world")
		err := os.WriteFile(filepath.Join(tmpdir, filename), content, 0o644)
		require.NoError(t, err)

		mockServer := new(mockGetFileServer)
		mockServer.On("Context").Return(t.Context())

		// Expect Start message
		mockServer.On("Send", mock.MatchedBy(func(resp *orchestrator.VolumeFileGetResponse) bool {
			_, ok := resp.Message.(*orchestrator.VolumeFileGetResponse_Start)
			return ok
		})).Return(nil).Once()

		// Expect Content message(s)
		mockServer.On("Send", mock.MatchedBy(func(resp *orchestrator.VolumeFileGetResponse) bool {
			c, ok := resp.Message.(*orchestrator.VolumeFileGetResponse_Content)
			if !ok {
				return false
			}
			return string(c.Content.Content) == "hello world"
		})).Return(nil).Once()

		// Expect Finish message
		mockServer.On("Send", mock.MatchedBy(func(resp *orchestrator.VolumeFileGetResponse) bool {
			_, ok := resp.Message.(*orchestrator.VolumeFileGetResponse_Finish)
			return ok
		})).Return(nil).Once()

		err = s.GetFile(&orchestrator.VolumeFileGetRequest{
			Volume: volumeInfo,
			Path:   filename,
		}, mockServer)

		require.NoError(t, err)
		mockServer.AssertExpectations(t)
	})

	t.Run("get non-existent file", func(t *testing.T) {
		mockServer := new(mockGetFileServer)
		mockServer.On("Context").Return(t.Context())

		err := s.GetFile(&orchestrator.VolumeFileGetRequest{
			Volume: volumeInfo,
			Path:   "non-existent",
		}, mockServer)

		require.Error(t, err)
	})
}
