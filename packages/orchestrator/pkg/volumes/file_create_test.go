package volumes

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type mockCreateFileServer struct {
	grpc.ServerStream
	mock.Mock
}

func (m *mockCreateFileServer) Recv() (*orchestrator.CreateFileRequest, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*orchestrator.CreateFileRequest), args.Error(1)
}

func (m *mockCreateFileServer) SendAndClose(resp *orchestrator.CreateFileResponse) error {
	args := m.Called(resp)

	return args.Error(0)
}

func (m *mockCreateFileServer) Context() context.Context {
	args := m.Called()

	return args.Get(0).(context.Context)
}

func TestFileCreate(t *testing.T) {
	t.Parallel()

	s, tmpdir, volumeInfo := setupTestService(t)

	t.Run("create file", func(t *testing.T) {
		t.Parallel()

		filename := "test-create.txt"
		mockServer := &mockCreateFileServer{}
		mockServer.On("Context").Return(t.Context())

		// 1. Send Start
		mockServer.On("Recv").Return(&orchestrator.CreateFileRequest{
			Message: &orchestrator.CreateFileRequest_Start{
				Start: &orchestrator.VolumeFileCreateStart{
					Volume: volumeInfo,
					Path:   filename,
				},
			},
		}, nil).Once()

		// 2. Send Content
		mockServer.On("Recv").Return(&orchestrator.CreateFileRequest{
			Message: &orchestrator.CreateFileRequest_Content{
				Content: &orchestrator.VolumeFileCreateContent{
					Content: []byte("hello world"),
				},
			},
		}, nil).Once()

		// 3. Send Finish
		mockServer.On("Recv").Return(&orchestrator.CreateFileRequest{
			Message: &orchestrator.CreateFileRequest_Finish{
				Finish: &orchestrator.VolumeFileCreateFinish{},
			},
		}, nil).Once()

		mockServer.On("SendAndClose", mock.Anything).Return(nil).Once()

		err := s.CreateFile(mockServer)
		require.NoError(t, err)

		content, err := os.ReadFile(filepath.Join(tmpdir, filename))
		require.NoError(t, err)
		require.Equal(t, "hello world", string(content))
		mockServer.AssertExpectations(t)
	})

	t.Run("create file with force", func(t *testing.T) {
		t.Parallel()

		filename := "nested/dir/test-create.txt"
		mockServer := &mockCreateFileServer{}
		mockServer.On("Context").Return(t.Context())

		mockServer.On("Recv").Return(&orchestrator.CreateFileRequest{
			Message: &orchestrator.CreateFileRequest_Start{
				Start: &orchestrator.VolumeFileCreateStart{
					Volume: volumeInfo,
					Path:   filename,
					Force:  true,
				},
			},
		}, nil).Once()

		mockServer.On("Recv").Return(&orchestrator.CreateFileRequest{
			Message: &orchestrator.CreateFileRequest_Finish{
				Finish: &orchestrator.VolumeFileCreateFinish{},
			},
		}, nil).Once()

		mockServer.On("SendAndClose", mock.Anything).Return(nil).Once()

		err := s.CreateFile(mockServer)
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(tmpdir, filename))
		require.NoError(t, err)
		mockServer.AssertExpectations(t)
	})

	t.Run("unexpected EOF", func(t *testing.T) {
		t.Parallel()

		mockServer := &mockCreateFileServer{}
		mockServer.On("Context").Return(t.Context())
		mockServer.On("Recv").Return(nil, io.EOF).Once()

		err := s.CreateFile(mockServer)
		require.Error(t, err)
	})
}
