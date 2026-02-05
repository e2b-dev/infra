package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
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

// 0 means "use the stored sandbox timeout" in the resume API.
const resumeTimeoutSeconds int32 = 0

func catalogResolution(ctx context.Context, sandboxId string, c catalog.SandboxesCatalog, pausedChecker PausedSandboxResumer, autoResumeEnabled bool) (string, error) {
	s, err := c.GetSandbox(ctx, sandboxId)
	if err != nil {
		if errors.Is(err, catalog.ErrSandboxNotFound) {
			if nodeIP, pausedErr := handlePausedSandbox(ctx, sandboxId, c, pausedChecker, autoResumeEnabled); pausedErr != nil {
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
	c catalog.SandboxesCatalog,
	pausedChecker PausedSandboxResumer,
	autoResumeEnabled bool,
) (string, error) {
	// Optimistic resume: try to resume directly without checking pause status first.
	// The server will return appropriate error codes based on sandbox state and policy.
	if pausedChecker == nil || !autoResumeEnabled {
		return "", nil
	}

	logger.L().Info(ctx, "attempting optimistic resume", logger.WithSandboxID(sandboxId))
	outcome, err := resumeOptimistically(ctx, sandboxId, pausedChecker)
	switch outcome {
	case resumeNotPaused:
		return "", nil
	case resumeAuthDenied:
		logger.L().Debug(ctx, "auto-resume not allowed", zap.Error(err), logger.WithSandboxID(sandboxId))

		return "", reverseproxy.NewErrSandboxPaused(sandboxId, false)
	case resumeFailed:
		logger.L().Warn(ctx, "auto-resume failed", zap.Error(err), logger.WithSandboxID(sandboxId))

		return "", reverseproxy.NewErrSandboxPaused(sandboxId, true)
	case resumeAlreadyRunning, resumeSucceeded:
		return resolveAfterResume(ctx, sandboxId, c)
	default:
		return "", reverseproxy.NewErrSandboxPaused(sandboxId, true)
	}
}

func isAuthResumeError(err error) bool {
	var grpcStatus interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &grpcStatus) {
		return false
	}

	switch grpcStatus.GRPCStatus().Code() {
	case codes.Unauthenticated, codes.PermissionDenied, codes.InvalidArgument, codes.FailedPrecondition:
		return true
	default:
		return false
	}
}

func isNotPausedError(err error) bool {
	var grpcStatus interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &grpcStatus) {
		return false
	}

	return grpcStatus.GRPCStatus().Code() == codes.NotFound
}

func isAlreadyRunningError(err error) bool {
	var grpcStatus interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &grpcStatus) {
		return false
	}

	return grpcStatus.GRPCStatus().Code() == codes.AlreadyExists
}

type resumeOutcome int

const (
	resumeSucceeded resumeOutcome = iota
	resumeNotPaused
	resumeAlreadyRunning
	resumeAuthDenied
	resumeFailed
)

func resumeOptimistically(ctx context.Context, sandboxId string, pausedChecker PausedSandboxResumer) (resumeOutcome, error) {
	err := pausedChecker.Resume(ctx, sandboxId, resumeTimeoutSeconds)
	if err == nil {
		return resumeSucceeded, nil
	}
	if isNotPausedError(err) {
		return resumeNotPaused, err
	}
	if isAlreadyRunningError(err) {
		return resumeAlreadyRunning, err
	}
	if isAuthResumeError(err) {
		return resumeAuthDenied, err
	}

	return resumeFailed, err
}

func resolveAfterResume(ctx context.Context, sandboxId string, c catalog.SandboxesCatalog) (string, error) {
	nodeIP, err := getCatalogIP(ctx, sandboxId, c)
	if err == nil {
		return nodeIP, nil
	}

	logger.L().Warn(ctx, "catalog lookup after resume failed", zap.Error(err), logger.WithSandboxID(sandboxId))

	return "", reverseproxy.NewErrSandboxPaused(sandboxId, true)
}

func getCatalogIP(ctx context.Context, sandboxId string, c catalog.SandboxesCatalog) (string, error) {
	s, err := c.GetSandbox(ctx, sandboxId)
	if err != nil {
		return "", err
	}

	return s.OrchestratorIP, nil
}

func NewClientProxy(meterProvider metric.MeterProvider, serviceName string, port uint16, catalog catalog.SandboxesCatalog, pausedChecker PausedSandboxResumer, autoResumeEnabled bool) (*reverseproxy.Proxy, error) {
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

			ctx = withProxyAuthMetadata(ctx, r.Header)
			nodeIP, err := catalogResolution(ctx, sandboxId, catalog, pausedChecker, autoResumeEnabled)
			if err != nil {
				if errors.Is(err, ErrNodeNotFound) {
					return nil, reverseproxy.NewErrSandboxNotFound(sandboxId)
				}

				if !errors.Is(err, ErrNodeNotFound) {
					l.Warn(ctx, "failed to resolve node ip with Redis resolution", zap.Error(err))
				}

				return nil, err
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
				OnProxyError:  pausedFallbackHandler(sandboxId, pausedChecker, autoResumeEnabled),
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

func pausedFallbackHandler(
	sandboxId string,
	pausedChecker PausedSandboxResumer,
	autoResumeEnabled bool,
) pool.ProxyErrorHandler {
	if pausedChecker == nil || !autoResumeEnabled {
		return nil
	}

	return func(_ http.ResponseWriter, r *http.Request, _ error) bool {
		ctx := withProxyAuthMetadata(context.WithoutCancel(r.Context()), r.Header)

		// Optimistic resume: try to resume in background without checking pause status
		go func() {
			resumeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			outcome, resumeErr := resumeOptimistically(resumeCtx, sandboxId, pausedChecker)
			switch outcome {
			case resumeFailed:
				logger.L().Warn(resumeCtx, "auto-resume failed after proxy error", zap.Error(resumeErr), logger.WithSandboxID(sandboxId))
			case resumeAuthDenied:
				logger.L().Debug(resumeCtx, "auto-resume not allowed after proxy error", zap.Error(resumeErr), logger.WithSandboxID(sandboxId))
			}
		}()

		return false
	}
}

func withProxyAuthMetadata(ctx context.Context, header http.Header) context.Context {
	md := metadata.New(nil)
	if value := strings.TrimSpace(header.Get("Authorization")); value != "" {
		md.Set("authorization", value)
	}
	if value := strings.TrimSpace(header.Get("X-API-Key")); value != "" {
		md.Set("x-api-key", value)
	}

	if len(md) == 0 {
		return ctx
	}

	return metadata.NewOutgoingContext(ctx, md)
}
