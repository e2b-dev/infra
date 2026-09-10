package grpc

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const panicSentinel = "panic-sentinel-not-for-callers-4b7e"

const fixedPanicMessage = "internal error"

type panickingService struct {
	proxygrpc.UnimplementedSandboxServiceServer
}

func (panickingService) ResumeSandbox(context.Context, *proxygrpc.SandboxResumeRequest) (*proxygrpc.SandboxResumeResponse, error) {
	panic(panicSentinel)
}

type respondingService struct {
	proxygrpc.UnimplementedSandboxServiceServer
}

func (respondingService) ResumeSandbox(context.Context, *proxygrpc.SandboxResumeRequest) (*proxygrpc.SandboxResumeResponse, error) {
	return &proxygrpc.SandboxResumeResponse{}, nil
}

//nolint:paralleltest // the middleware logs through the global logger, which this test replaces
func TestNewGRPCServerRecoveryHandler(t *testing.T) {
	logs := captureLogs(t)

	client := serveSandboxService(t, panickingService{}, WithRecoveryHandler(func(any) error {
		return status.Error(codes.Internal, fixedPanicMessage)
	}))

	_, err := client.ResumeSandbox(t.Context(), &proxygrpc.SandboxResumeRequest{})

	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, fixedPanicMessage, status.Convert(err).Message())
	require.NotContains(t, err.Error(), panicSentinel)
	require.NotContains(t, flattenLogs(logs), panicSentinel)
}

//nolint:paralleltest // the middleware logs through the global logger, which this test replaces
func TestNewGRPCServerWithoutPayloadLogging(t *testing.T) {
	logs := captureLogs(t)
	client := serveSandboxService(t, respondingService{}, WithoutPayloadLogging())

	_, err := client.ResumeSandbox(t.Context(), &proxygrpc.SandboxResumeRequest{SandboxId: panicSentinel})

	require.NoError(t, err)
	require.NotContains(t, flattenLogs(logs), panicSentinel)
}

//nolint:paralleltest // the middleware logs through the global logger, which this test replaces
func TestNewGRPCServerWithUnaryInterceptors(t *testing.T) {
	captureLogs(t)
	rejected := status.Error(codes.InvalidArgument, "rejected by interceptor")
	client := serveSandboxService(t, respondingService{}, WithUnaryInterceptors(
		func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
			return nil, rejected
		},
	))

	_, err := client.ResumeSandbox(t.Context(), &proxygrpc.SandboxResumeRequest{})

	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "rejected by interceptor", status.Convert(err).Message())
}

func serveSandboxService(t *testing.T, service proxygrpc.SandboxServiceServer, opts ...ServerOption) proxygrpc.SandboxServiceClient {
	t.Helper()

	server := NewGRPCServer(&telemetry.Client{
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
	}, opts...)
	proxygrpc.RegisterSandboxServiceServer(server, service)

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return proxygrpc.NewSandboxServiceClient(conn)
}

func captureLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()

	core, logs := observer.New(zap.DebugLevel)
	restore := logger.ReplaceGlobals(t.Context(), logger.NewTracedLoggerFromCore(core))
	t.Cleanup(restore)

	return logs
}

func flattenLogs(logs *observer.ObservedLogs) string {
	var out strings.Builder
	for _, entry := range logs.All() {
		out.WriteString(entry.Message)

		for key, value := range entry.ContextMap() {
			out.WriteString(key)
			fmt.Fprint(&out, value)
		}
	}

	return out.String()
}
