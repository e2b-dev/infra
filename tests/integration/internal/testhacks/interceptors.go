package testhacks

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func GRPCUnaryInterceptor(tb testing.TB) grpc.UnaryClientInterceptor {
	tb.Helper()

	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-test-name", tb.Name())
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func GRPCStreamInterceptor(tb testing.TB) grpc.StreamClientInterceptor {
	tb.Helper()

	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-test-name", tb.Name())
		return streamer(ctx, desc, cc, method, opts...)
	}
}
