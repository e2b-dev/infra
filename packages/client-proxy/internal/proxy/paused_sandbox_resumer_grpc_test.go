package proxy

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

type fakeSandboxServer struct {
	proxygrpc.UnimplementedSandboxServiceServer

	resp *proxygrpc.SandboxResumeResponse
	err  error

	gotSandboxID string
	gotMetadata  metadata.MD
	calls        atomic.Int32
}

func (f *fakeSandboxServer) ResumeSandbox(ctx context.Context, req *proxygrpc.SandboxResumeRequest) (*proxygrpc.SandboxResumeResponse, error) {
	f.calls.Add(1)
	f.gotSandboxID = req.GetSandboxId()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.gotMetadata = md
	}

	if f.err != nil {
		return nil, f.err
	}

	return f.resp, nil
}

func startFakeServer(t *testing.T, srv proxygrpc.SandboxServiceServer) *grpc.ClientConn {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	proxygrpc.RegisterSandboxServiceServer(server, srv)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
	})

	return conn
}

func TestNewGRPCPausedSandboxResumer_EmptyAddressErrors(t *testing.T) {
	t.Parallel()

	r, err := NewGRPCPausedSandboxResumer(t.Context(), "  ", GRPCOAuthConfig{}, false)
	require.Error(t, err)
	require.Nil(t, r)
}

func TestNewGRPCPausedSandboxResumer_OAuthMisconfiguredErrors(t *testing.T) {
	t.Parallel()

	r, err := NewGRPCPausedSandboxResumer(t.Context(), "127.0.0.1:1234", GRPCOAuthConfig{ClientID: "only-id"}, false)
	require.Error(t, err)
	require.Nil(t, r)
}

func TestNewGRPCPausedSandboxResumer_InsecureSucceeds(t *testing.T) {
	t.Parallel()

	r, err := NewGRPCPausedSandboxResumer(t.Context(), "127.0.0.1:1234", GRPCOAuthConfig{}, false)
	require.NoError(t, err)
	require.NotNil(t, r)

	closer, ok := r.(interface {
		Close(ctx context.Context) error
	})
	require.True(t, ok)
	require.NoError(t, closer.Close(t.Context()))
}

func TestNewGRPCPausedSandboxResumer_TLSSucceeds(t *testing.T) {
	t.Parallel()

	r, err := NewGRPCPausedSandboxResumer(t.Context(), "127.0.0.1:1234", GRPCOAuthConfig{}, true)
	require.NoError(t, err)
	require.NotNil(t, r)

	closer, ok := r.(interface {
		Close(ctx context.Context) error
	})
	require.True(t, ok)
	require.NoError(t, closer.Close(t.Context()))
}

func TestGRPCPausedSandboxResumer_ResumeSendsMetadataAndReturnsIP(t *testing.T) {
	t.Parallel()

	srv := &fakeSandboxServer{
		resp: &proxygrpc.SandboxResumeResponse{OrchestratorIp: "  10.0.0.5  "},
	}
	conn := startFakeServer(t, srv)

	r := &grpcPausedSandboxResumer{
		conn:   conn,
		client: proxygrpc.NewSandboxServiceClient(conn),
		auth:   noopGrpcResumeAuth{},
	}

	ip, err := r.Resume(t.Context(), "sbx-123", 49983, "traffic-token", "envd-token")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.5", ip)
	require.EqualValues(t, 1, srv.calls.Load())
	require.Equal(t, "sbx-123", srv.gotSandboxID)
	require.Equal(t, []string{"49983"}, srv.gotMetadata.Get(proxygrpc.MetadataSandboxRequestPort))
	require.Equal(t, []string{"traffic-token"}, srv.gotMetadata.Get(proxygrpc.MetadataTrafficAccessToken))
	require.Equal(t, []string{"envd-token"}, srv.gotMetadata.Get(proxygrpc.MetadataEnvdAccessToken))
}

func TestGRPCPausedSandboxResumer_ResumeOmitsEmptyTokens(t *testing.T) {
	t.Parallel()

	srv := &fakeSandboxServer{
		resp: &proxygrpc.SandboxResumeResponse{OrchestratorIp: "10.0.0.6"},
	}
	conn := startFakeServer(t, srv)

	r := &grpcPausedSandboxResumer{
		conn:   conn,
		client: proxygrpc.NewSandboxServiceClient(conn),
		auth:   noopGrpcResumeAuth{},
	}

	ip, err := r.Resume(t.Context(), "sbx-456", 8080, "", "")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.6", ip)
	require.EqualValues(t, 1, srv.calls.Load())
	require.Empty(t, srv.gotMetadata.Get(proxygrpc.MetadataTrafficAccessToken))
	require.Empty(t, srv.gotMetadata.Get(proxygrpc.MetadataEnvdAccessToken))
	require.Equal(t, []string{"8080"}, srv.gotMetadata.Get(proxygrpc.MetadataSandboxRequestPort))
}

func TestGRPCPausedSandboxResumer_ResumeReturnsServerError(t *testing.T) {
	t.Parallel()

	srv := &fakeSandboxServer{
		err: status.Error(codes.NotFound, "missing"),
	}
	conn := startFakeServer(t, srv)

	r := &grpcPausedSandboxResumer{
		conn:   conn,
		client: proxygrpc.NewSandboxServiceClient(conn),
		auth:   noopGrpcResumeAuth{},
	}

	ip, err := r.Resume(t.Context(), "sbx", 80, "", "")
	require.Error(t, err)
	require.Empty(t, ip)
	st, ok := status.FromError(errors.Unwrap(err))
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
	require.EqualValues(t, 1, srv.calls.Load())
}

type errAuth struct {
	err error
}

func (e errAuth) authorize(_ context.Context) (context.Context, error) {
	return nil, e.err
}

func TestGRPCPausedSandboxResumer_ResumeAuthError(t *testing.T) {
	t.Parallel()

	srv := &fakeSandboxServer{
		resp: &proxygrpc.SandboxResumeResponse{OrchestratorIp: "10.0.0.7"},
	}
	conn := startFakeServer(t, srv)

	authErr := errors.New("auth failed")
	r := &grpcPausedSandboxResumer{
		conn:   conn,
		client: proxygrpc.NewSandboxServiceClient(conn),
		auth:   errAuth{err: authErr},
	}

	ip, err := r.Resume(t.Context(), "sbx", 80, "", "")
	require.ErrorIs(t, err, authErr)
	require.Empty(t, ip)
	require.EqualValues(t, 0, srv.calls.Load())
}

func TestGRPCPausedSandboxResumer_Init(t *testing.T) {
	t.Parallel()

	srv := &fakeSandboxServer{}
	conn := startFakeServer(t, srv)

	r := &grpcPausedSandboxResumer{
		conn:   conn,
		client: proxygrpc.NewSandboxServiceClient(conn),
		auth:   noopGrpcResumeAuth{},
	}

	// Should not panic
	r.Init(t.Context())
}

func TestGRPCPausedSandboxResumer_Close(t *testing.T) {
	t.Parallel()

	srv := &fakeSandboxServer{}
	conn := startFakeServer(t, srv)

	r := &grpcPausedSandboxResumer{
		conn:   conn,
		client: proxygrpc.NewSandboxServiceClient(conn),
		auth:   noopGrpcResumeAuth{},
	}

	require.NoError(t, r.Close(t.Context()))
}
