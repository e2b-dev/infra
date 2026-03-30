package grpc

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	connectionMetricsOnce sync.Once

	connectionStateDuration metric.Int64Histogram
	errConnectionMetrics    error
)

func initConnectionMetrics() error {
	connectionMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/e2b-dev/infra/packages/shared/pkg/grpc")

		connectionStateDuration, errConnectionMetrics = meter.Int64Histogram(
			"grpc.client.connection.state.duration",
			metric.WithDescription("Time spent in each gRPC client connectivity state before transitioning"),
			metric.WithUnit("ms"),
		)
		if errConnectionMetrics != nil {
			return
		}
	})

	return errConnectionMetrics
}

//nolint:contextcheck // long-lived connection observer intentionally decouples from request lifecycles
func ObserveConnection(ctx context.Context, conn *grpc.ClientConn, target string) {
	if conn == nil {
		return
	}

	if ctx == nil {
		ctx = context.TODO()
	}

	if err := initConnectionMetrics(); err != nil {
		logger.L().Warn(ctx, "failed to initialize gRPC connection observability metrics", zap.Error(err))

		return
	}

	target = strings.TrimSpace(target)
	if target == "" {
		target = "unknown"
	}

	RegisterChannelzTarget(conn, target)

	observeCtx := context.WithoutCancel(ctx)

	go observeConnection(observeCtx, conn, target)
}

func observeConnection(ctx context.Context, conn *grpc.ClientConn, target string) {
	state := conn.GetState()
	stateStart := time.Now()

	for {
		if !conn.WaitForStateChange(ctx, state) {
			recordStateDuration(ctx, target, state, time.Since(stateStart))

			return
		}

		nextState := conn.GetState()
		recordStateDuration(ctx, target, state, time.Since(stateStart))

		if nextState == connectivity.Shutdown {
			return
		}

		state = nextState
		stateStart = time.Now()
	}
}

func recordStateDuration(ctx context.Context, target string, state connectivity.State, duration time.Duration) {
	connectionStateDuration.Record(ctx, duration.Milliseconds(), metric.WithAttributes(
		attribute.String("grpc.target", target),
		attribute.String("grpc.connectivity.state", state.String()),
	))
}
