package testhacks

import (
	"context"
	"fmt"

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

func getTestName(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}

	testName := md.Get("x-test-name")
	if len(testName) == 0 {
		return ""
	}

	return testName[0]
}

func UnaryTestNamePrinter(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	testName := getTestName(ctx)
	if testName != "" {
		fmt.Printf("====================== START client-proxy unary for %s ========================", testName)
	}

	resp, err = handler(ctx, req)
	if testName != "" {
		fmt.Printf("====================== FINISH client-proxy unary for %s ========================", testName)
	}

	return resp, err
}

func StreamingTestNamePrinter(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	testName := getTestName(ss.Context())
	if testName != "" {
		fmt.Printf("====================== START client-proxy streaming for %s ========================", testName)
	}

	err := handler(srv, ss)

	if testName != "" {
		fmt.Printf("====================== FINISH client-proxy streaming for %s ========================", testName)
	}

	return err
}
