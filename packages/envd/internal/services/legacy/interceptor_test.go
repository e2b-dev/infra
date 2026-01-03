package legacy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
	spec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem/filesystemconnect"
	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem/filesystemconnect/mocks"
)

func TestInterceptor(t *testing.T) {
	t.Parallel()
	streamSetup := func(count int) func(mockFS *filesystemconnectmocks.MockFilesystemHandler) {
		return func(mockFS *filesystemconnectmocks.MockFilesystemHandler) {
			mockFS.EXPECT().
				WatchDir(mock.Anything, mock.Anything, mock.Anything).
				Run(func(_ context.Context, _ *connect.Request[filesystem.WatchDirRequest], serverStream *connect.ServerStream[filesystem.WatchDirResponse]) {
					for range count {
						err := serverStream.Send(&filesystem.WatchDirResponse{Event: &filesystem.WatchDirResponse_Start{Start: &filesystem.WatchDirResponse_StartEvent{}}})
						require.NoError(t, err)
					}
				}).
				Return(nil)
		}
	}

	streamTest := func(count int) func(t *testing.T, client spec.FilesystemClient) http.Header {
		t.Helper()

		return func(t *testing.T, client spec.FilesystemClient) http.Header {
			t.Helper()

			req := connect.NewRequest[filesystem.WatchDirRequest](&filesystem.WatchDirRequest{Path: "/a/b/c"})
			resp, err := client.WatchDir(t.Context(), req)
			require.NoError(t, err)

			for range count {
				ok := resp.Receive()
				require.True(t, ok, resp.Err())

				item := resp.Msg()
				require.NotNil(t, item)
			}

			return resp.ResponseHeader()
		}
	}

	unarySetup := func(mockFS *filesystemconnectmocks.MockFilesystemHandler) {
		mockFS.EXPECT().
			ListDir(mock.Anything, mock.Anything).
			Return(&connect.Response[filesystem.ListDirResponse]{Msg: &filesystem.ListDirResponse{}}, nil)
	}

	unaryTest := func(t *testing.T, client spec.FilesystemClient) http.Header {
		t.Helper()

		req := connect.NewRequest[filesystem.ListDirRequest](&filesystem.ListDirRequest{Path: "/a/b/c"})
		resp, err := client.ListDir(t.Context(), req)
		require.NoError(t, err)

		return resp.Header()
	}

	tests := map[string]struct {
		userAgent         string
		setup             func(mockFS *filesystemconnectmocks.MockFilesystemHandler)
		execute           func(t *testing.T, client spec.FilesystemClient) http.Header
		expectHeaderValue string
	}{
		"streaming interceptor converts no messages": {
			userAgent:         brokenUserAgent,
			setup:             streamSetup(0),
			execute:           streamTest(0),
			expectHeaderValue: "true",
		},
		"streaming interceptor converts single messages": {
			userAgent:         brokenUserAgent,
			setup:             streamSetup(1),
			execute:           streamTest(1),
			expectHeaderValue: "true",
		},
		"streaming interceptor converts multiple messages": {
			userAgent:         brokenUserAgent,
			setup:             streamSetup(2),
			execute:           streamTest(2),
			expectHeaderValue: "true",
		},
		"streaming interceptor can avoid conversion": {
			setup:   streamSetup(1),
			execute: streamTest(1),
		},
		"unary interceptor converts when necessary": {
			userAgent:         brokenUserAgent,
			setup:             unarySetup,
			execute:           unaryTest,
			expectHeaderValue: "true",
		},
		"unary interceptor can avoid conversion": {
			setup:   unarySetup,
			execute: unaryTest,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// setup server
			mockFS := filesystemconnectmocks.NewMockFilesystemHandler(t)
			if test.setup != nil {
				test.setup(mockFS)
			}
			_, handler := spec.NewFilesystemHandler(
				mockFS, connect.WithInterceptors(Convert()),
			)
			srv := httptest.NewServer(handler)
			t.Cleanup(func() { srv.Close() })

			// setup client
			var clientOptions []connect.ClientOption
			if test.userAgent != "" {
				clientOptions = append(clientOptions, connect.WithInterceptors(addUserAgent(test.userAgent)))
			}
			client := spec.NewFilesystemClient(srv.Client(), srv.URL, clientOptions...)

			// make request
			responseHeaders := test.execute(t, client)

			// verify results; this ensures we don't set this header multiple times
			actualHeaderValues := responseHeaders.Values(notifyHeader)
			if test.expectHeaderValue != "" {
				assert.Equal(t, []string{test.expectHeaderValue}, actualHeaderValues)
			} else {
				assert.Empty(t, actualHeaderValues)
			}
		})
	}
}

// userAgentInterceptor adds a request header to the client
type userAgentInterceptor struct {
	userAgent string
}

func (u userAgentInterceptor) WrapUnary(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		request.Header().Set("User-Agent", u.userAgent)

		return unaryFunc(ctx, request)
	}
}

func (u userAgentInterceptor) WrapStreamingClient(clientFunc connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, s connect.Spec) connect.StreamingClientConn {
		conn := clientFunc(ctx, s)
		conn.RequestHeader().Set("user-agent", u.userAgent)

		return conn
	}
}

func (u userAgentInterceptor) WrapStreamingHandler(connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	// TODO implement me
	panic("implement me")
}

var _ connect.Interceptor = (*userAgentInterceptor)(nil)

func addUserAgent(userAgent string) connect.Interceptor {
	return &userAgentInterceptor{userAgent: userAgent}
}
