package grpc

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
)

// UnaryTimeoutInterceptor returns a gRPC unary server interceptor that applies
// the given timeout to each request context.
func UnaryTimeoutInterceptor(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("request timed out"))
		defer cancel()

		return handler(ctx, req)
	}
}
