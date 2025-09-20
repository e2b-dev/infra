package testhacks

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func GRPCUnaryInterceptor(ctx context.Context, method string, req any, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if testName := GetTestName(ctx); testName != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-test-name", testName)
	}

	return invoker(ctx, method, req, reply, cc, opts...)
}

func GRPCStreamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	if testName := GetTestName(ctx); testName != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-test-name", testName)
	}

	return streamer(ctx, desc, cc, method, opts...)
}
