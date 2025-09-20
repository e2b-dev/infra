package testhacks

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

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
		fmt.Printf("====================== START orchestrator unary for %s ========================\n", testName)
	}

	resp, err = handler(ctx, req)
	if testName != "" {
		fmt.Printf("====================== FINISH orchestrator unary for %s ========================\n", testName)
	}

	return resp, err
}

func StreamingTestNamePrinter(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	testName := getTestName(ss.Context())
	if testName != "" {
		fmt.Printf("====================== START orchestrator streaming for %s ========================\n", testName)
	}

	err := handler(srv, ss)

	if testName != "" {
		fmt.Printf("====================== FINISH orchestrator streaming for %s ========================\n", testName)
	}

	return err
}
