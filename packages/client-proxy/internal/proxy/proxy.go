package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	orchestratorProxyPort = 5007 // orchestrator proxy port

	// This timeout should be > 600 (GCP LB upstream idle timeout) to prevent race condition
	// Also it's a good practice to set it to a value higher than the idle timeout of the backend service
	// https://cloud.google.com/load-balancing/docs/https#timeouts_and_retries%23:~:text=The%20load%20balancer%27s%20backend%20keepalive,is%20greater%20than%20600%20seconds
	idleTimeout = 610 * time.Second
)

var ErrNodeNotFound = errors.New("node not found")

// use 5 minutes for now; will allow settings for this later
const resumeTimeoutSeconds int32 = 300

func catalogResolution(ctx context.Context, sandboxId string, c catalog.SandboxesCatalog, pausedChecker PausedSandboxResumer, featureFlags *featureflags.Client) (string, error) {
	s, err := c.GetSandbox(ctx, sandboxId)
	if err != nil {
		if errors.Is(err, catalog.ErrSandboxNotFound) {
			if nodeIP, pausedErr := handlePausedSandbox(ctx, sandboxId, pausedChecker, featureFlags); pausedErr != nil {
				return "", pausedErr
			} else if nodeIP != "" {
				return nodeIP, nil
			}

			return "", ErrNodeNotFound
		}

		return "", fmt.Errorf("failed to get sandbox from catalog: %w", err)
	}

	// todo: when we will use edge for orchestrators discovery we can stop sending IP in the catalog
	//  and just resolve node from pool to get the IP of the node
	return s.OrchestratorIP, nil
}

func handlePausedSandbox(
	ctx context.Context,
	sandboxId string,
	pausedChecker PausedSandboxResumer,
	featureFlags *featureflags.Client,
) (string, error) {
	if pausedChecker == nil {
		return "", nil
	}

	if !featureFlags.BoolFlag(ctx, featureflags.SandboxAutoResumeFlag, featureflags.SandboxContext(sandboxId)) {
		logger.L().Debug(ctx, "sandbox auto-resume disabled; skipping api resume", logger.WithSandboxID(sandboxId))

		return "", nil
	}

	logger.L().Info(ctx, "catalog miss, attempting resume via api", logger.WithSandboxID(sandboxId))
	nodeIP, err := pausedChecker.Resume(ctx, sandboxId, resumeTimeoutSeconds)
	if err == nil {
		return nodeIP, nil
	}

	if isNotPausedError(err) {
		// API says it can't resume (no snapshot / not resumable).
		return "", nil
	}

	return "", err
}

func isNotPausedError(err error) bool {
	var grpcStatus interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &grpcStatus) {
		return false
	}

	return grpcStatus.GRPCStatus().Code() == codes.NotFound
}

func NewClientProxy(meterProvider metric.MeterProvider, serviceName string, port uint16, catalog catalog.SandboxesCatalog, pausedSandboxResumer PausedSandboxResumer, featureFlagsClient *featureflags.Client) (*reverseproxy.Proxy, error) {
	getTargetFromRequest := reverseproxy.GetTargetFromRequest(env.IsLocal())

	proxy := reverseproxy.New(
		port,
		// Retries that are needed to handle port forwarding delays in sandbox envd are handled by the orchestrator proxy
		reverseproxy.ClientProxyRetries,
		idleTimeout,
		func(r *http.Request) (*pool.Destination, error) {
			ctx := r.Context()
			sandboxId, port, err := getTargetFromRequest(r)
			if err != nil {
				return nil, err
			}

			l := logger.L().With(
				zap.String("origin_host", r.Host),
				logger.WithSandboxID(sandboxId),
				zap.Uint64("sandbox_req_port", port),
				zap.String("sandbox_req_path", r.URL.Path),
				zap.String("sandbox_req_method", r.Method),
				zap.String("sandbox_req_user_agent", r.UserAgent()),
				zap.String("remote_addr", r.RemoteAddr),
				zap.Int64("content_length", r.ContentLength),
			)

			nodeIP, err := catalogResolution(ctx, sandboxId, catalog, pausedSandboxResumer, featureFlagsClient)
			if err != nil {
				if !errors.Is(err, ErrNodeNotFound) {
					l.Warn(ctx, "failed to resolve node ip with Redis resolution", zap.Error(err))
				}

				return nil, reverseproxy.NewErrSandboxNotFound(sandboxId)
			}

			url := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s:%d", nodeIP, orchestratorProxyPort),
			}

			l = l.With(
				zap.String("target_hostname", url.Hostname()),
				zap.String("target_port", url.Port()),
			)

			return &pool.Destination{
				SandboxId:     sandboxId,
				RequestLogger: l,
				SandboxPort:   port,
				ConnectionKey: pool.ClientProxyConnectionKey,
				Url:           url,
			}, nil
		},
		false,
	)

	meter := meterProvider.Meter(serviceName)
	_, err := telemetry.GetObservableUpDownCounter(
		meter, telemetry.ClientProxyPoolConnectionsMeterCounterName, func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(proxy.CurrentServerConnections())

			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error registering client proxy connections metric (%s): %w", telemetry.ClientProxyPoolConnectionsMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(
		meter, telemetry.ClientProxyPoolSizeMeterCounterName, func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(int64(proxy.CurrentPoolSize()))

			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error registering client proxy pool size metric (%s): %w", telemetry.ClientProxyPoolSizeMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(
		meter, telemetry.ClientProxyServerConnectionsMeterCounterName, func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(proxy.CurrentPoolConnections())

			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error registering client proxy server connections metric (%s): %w", telemetry.ClientProxyServerConnectionsMeterCounterName, err)
	}

	return proxy, nil
}
