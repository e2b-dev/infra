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
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/template"
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

var (
	resumeWaitInterval         = 200 * time.Millisecond
	resumeWaitTimeout          = 30 * time.Second
	resumeTimeoutSeconds int32 = 600
)

func catalogResolution(ctx context.Context, sandboxId string, c catalog.SandboxesCatalog, pausedChecker PausedSandboxChecker, autoResumeEnabled bool, requestHasAuth bool) (string, error) {
	s, err := c.GetSandbox(ctx, sandboxId)
	if err != nil {
		if errors.Is(err, catalog.ErrSandboxNotFound) {
			if nodeIP, pausedErr := handlePausedSandbox(ctx, sandboxId, c, pausedChecker, autoResumeEnabled, requestHasAuth); pausedErr != nil {
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
	pausedChecker PausedSandboxChecker,
	autoResumeEnabled bool,
	requestHasAuth bool,
) (string, error) {
	// Centralizes paused-sandbox handling to keep catalog resolution linear and easier to test.
	// Ask the paused catalog if the sandbox exists and is paused.
	pausedInfo, pausedFound := getPausedInfo(ctx, sandboxId, pausedChecker)
	if !pausedFound {
		return "", nil
	}

	return handlePausedSandboxWithInfo(ctx, sandboxId, c, pausedChecker, autoResumeEnabled, requestHasAuth, pausedInfo)
}

func handlePausedSandboxWithInfo(
	ctx context.Context,
	sandboxId string,
	c catalog.SandboxesCatalog,
	pausedChecker PausedSandboxChecker,
	autoResumeEnabled bool,
	requestHasAuth bool,
	pausedInfo PausedInfo,
) (string, error) {
	// Shared implementation so callers can reuse pre-fetched paused info.
	logSleeping(ctx, sandboxId)

	// Decide if we are allowed to auto-resume for this request.
	canAutoResume := shouldAutoResume(pausedInfo.AutoResumePolicy, autoResumeEnabled, requestHasAuth) && pausedChecker != nil
	if canAutoResume {
		logger.L().Info(ctx, "auto-resuming sandbox", logger.WithSandboxID(sandboxId))
		if err := pausedChecker.Resume(ctx, sandboxId, resumeTimeoutSeconds); err != nil {
			logger.L().Warn(ctx, "auto-resume failed", zap.Error(err), logger.WithSandboxID(sandboxId))
			if isAuthResumeError(err) {
				canAutoResume = false
			}
		} else {
			nodeIP, waitErr := waitForCatalog(ctx, sandboxId, c)
			if waitErr == nil {
				return nodeIP, nil
			}
			logger.L().Warn(ctx, "auto-resume wait failed", zap.Error(waitErr), logger.WithSandboxID(sandboxId))
		}
	}

	return "", reverseproxy.NewErrSandboxPaused(sandboxId, canAutoResume)
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

func getPausedInfo(ctx context.Context, sandboxId string, pausedChecker PausedSandboxChecker) (PausedInfo, bool) {
	// Isolates paused lookup and logging so catalogResolution doesn't need to care about error cases.
	if pausedChecker == nil {
		return PausedInfo{}, false
	}

	info, err := pausedChecker.PausedInfo(ctx, sandboxId)
	if err != nil {
		logger.L().Warn(ctx, "paused lookup failed", zap.Error(err), logger.WithSandboxID(sandboxId))

		return PausedInfo{}, false
	}
	if !info.Paused {
		return PausedInfo{}, false
	}

	return info, true
}

func waitForCatalog(ctx context.Context, sandboxId string, c catalog.SandboxesCatalog) (string, error) {
	deadline := time.Now().Add(resumeWaitTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for sandbox to resume")
		}

		s, err := c.GetSandbox(ctx, sandboxId)
		if err == nil {
			return s.OrchestratorIP, nil
		}

		if !errors.Is(err, catalog.ErrSandboxNotFound) {
			return "", fmt.Errorf("failed to get sandbox from catalog during resume: %w", err)
		}

		wait := min(time.Until(deadline), resumeWaitInterval)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()

			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func shouldAutoResume(_ proxygrpc.AutoResumePolicy, autoResumeEnabled bool, _ bool) bool {
	return autoResumeEnabled
}

func NewClientProxy(meterProvider metric.MeterProvider, serviceName string, port uint16, catalog catalog.SandboxesCatalog) (*reverseproxy.Proxy, error) {
	return NewClientProxyWithPausedChecker(meterProvider, serviceName, port, catalog, nil, true)
}

func NewClientProxyWithPausedChecker(meterProvider metric.MeterProvider, serviceName string, port uint16, catalog catalog.SandboxesCatalog, pausedChecker PausedSandboxChecker, autoResumeEnabled bool) (*reverseproxy.Proxy, error) {
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

			requestHasAuth := hasProxyAuth(r.Header)
			ctx = withProxyAuthMetadata(ctx, r.Header)
			nodeIP, err := catalogResolution(ctx, sandboxId, catalog, pausedChecker, autoResumeEnabled, requestHasAuth)
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
				OnProxyError:  pausedFallbackHandler(sandboxId, pausedChecker, autoResumeEnabled, requestHasAuth),
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
	pausedChecker PausedSandboxChecker,
	autoResumeEnabled bool,
	requestHasAuth bool,
) pool.ProxyErrorHandler {
	if pausedChecker == nil {
		return nil
	}

	return func(w http.ResponseWriter, r *http.Request, _ error) bool {
		ctx := withProxyAuthMetadata(context.WithoutCancel(r.Context()), r.Header)
		pausedInfo, pausedFound := getPausedInfo(ctx, sandboxId, pausedChecker)
		if !pausedFound {
			return false
		}

		canAutoResume := shouldAutoResume(pausedInfo.AutoResumePolicy, autoResumeEnabled, requestHasAuth)
		if canAutoResume {
			go func() {
				resumeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()

				if resumeErr := pausedChecker.Resume(resumeCtx, sandboxId, resumeTimeoutSeconds); resumeErr != nil {
					logger.L().Warn(resumeCtx, "auto-resume failed after proxy error", zap.Error(resumeErr), logger.WithSandboxID(sandboxId))
				}
			}()

			return false
		}

		if handleErr := template.NewSandboxPausedError(sandboxId, r.Host, false).HandleError(w, r); handleErr != nil {
			logger.L().Error(ctx, "failed to handle sandbox paused error", zap.Error(handleErr), logger.WithSandboxID(sandboxId))
			http.Error(w, "Failed to handle sandbox paused error", http.StatusInternalServerError)
		}

		return true
	}
}

func hasProxyAuth(header http.Header) bool {
	if strings.TrimSpace(header.Get("Authorization")) != "" {
		return true
	}
	if strings.TrimSpace(header.Get("X-API-Key")) != "" {
		return true
	}

	return false
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
