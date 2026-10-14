package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// This timeout should be > 600 (GCP LB upstream idle timeout) to prevent race condition
	// Also it's a good practice to set it to a value higher than the idle timeout of the backend service
	// https://cloud.google.com/load-balancing/docs/https#timeouts_and_retries%23:~:text=The%20load%20balancer%27s%20backend%20keepalive,is%20greater%20than%20600%20seconds
	idleTimeout = 610 * time.Second
)

var (
	ErrNodeNotFound         = errors.New("node not found")
	ErrNodeRouteUnavailable = errors.New("node route unavailable")
)

type autoResumeResult uint8

const (
	autoResumeSucceeded autoResumeResult = iota
	autoResumeNotAllowed
	autoResumePermissionDenied
	autoResumeResourceExhausted
	autoResumeErrored
)

func catalogSandboxNodeIP(s *catalog.SandboxInfo) (string, error) {
	return normalizeNodeIP(s.OrchestratorIP)
}

func normalizeNodeIP(nodeIP string) (string, error) {
	nodeIP = strings.TrimSpace(nodeIP)
	if nodeIP == "" {
		return "", ErrNodeRouteUnavailable
	}

	return nodeIP, nil
}

func clientProxyMaskRequestHost(ctx context.Context, featureFlags *featureflags.Client, host string, sandboxID string, port uint64) *string {
	domain, sharedHost := reverseproxy.SandboxSharedHostDomain(host)
	if !sharedHost || featureFlags.BoolFlag(ctx, featureflags.OrchAcceptsCombinedHostFlag) {
		return nil
	}

	orchestratorHost := fmt.Sprintf("%d-%s.%s", port, sandboxID, domain)

	return &orchestratorHost
}

func catalogResolution(ctx context.Context, sandboxId string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string, c catalog.SandboxesCatalog, pausedChecker SandboxResumer) (string, error) {
	s, err := c.GetSandbox(ctx, sandboxId)
	if err != nil {
		if errors.Is(err, catalog.ErrSandboxNotFound) {
			nodeIP, res, pausedErr := handlePausedSandbox(ctx, sandboxId, sandboxPort, trafficAccessToken, envdAccessToken, pausedChecker)
			if pausedErr != nil {
				return "", pausedErr
			}
			if res == autoResumeSucceeded {
				return nodeIP, nil
			}

			return "", ErrNodeNotFound
		}

		return "", fmt.Errorf("failed to get sandbox from catalog: %w", err)
	}

	return catalogSandboxNodeIP(s)
}

func handlePausedSandbox(
	ctx context.Context,
	sandboxId string,
	sandboxPort uint64,
	trafficAccessToken string,
	envdAccessToken string,
	pausedChecker SandboxResumer,
) (string, autoResumeResult, error) {
	if pausedChecker == nil {
		return "", autoResumeNotAllowed, nil
	}

	nodeIP, err := pausedChecker.Resume(ctx, sandboxId, sandboxPort, trafficAccessToken, envdAccessToken)
	if err != nil {
		if st, ok := resumeStatus(err); ok {
			if st.Code() == codes.PermissionDenied {
				return "", autoResumePermissionDenied, reverseproxy.NewErrSandboxResumePermissionDenied(sandboxId)
			}
			if st.Code() == codes.NotFound {
				return "", autoResumeNotAllowed, nil
			}
			if st.Code() == codes.FailedPrecondition && st.Message() == proxygrpc.SandboxStillTransitioningMessage {
				// The SDK reads an envd-port 409 as "already exists" (a
				// make_dir then reports success), so envd traffic gets
				// the same page as 503, which it reads as unavailable.
				if sandboxPort == uint64(consts.DefaultEnvdServerPort) {
					return "", autoResumeErrored, reverseproxy.NewErrSandboxStillTransitioningUnavailable(sandboxId)
				}

				return "", autoResumeErrored, reverseproxy.NewErrSandboxStillTransitioning(sandboxId)
			}
			if st.Code() == codes.ResourceExhausted {
				return "", autoResumeResourceExhausted, reverseproxy.NewErrSandboxResourceExhausted(sandboxId, st.Message())
			}
			// The code the API sent decides a placement outcome: a refusal
			// no retry helps gets the refused page whatever its gRPC code
			// (a node's refusal travels as 500 or 400), a placement timeout
			// (504) and a create that failed on the node (500) the capacity
			// page, since the not-found page they would fall to reads as
			// the sandbox being gone.
			code, permanent := resumeRefusalCode(st)
			if permanent {
				// The API's own status for the refusal, carried on its
				// detail; from an API that sends none, a bad request stays
				// a 400 the SDKs do not retry and anything else is the 503
				// page.
				statusCode := resumeHTTPStatus(st)
				if statusCode == 0 {
					statusCode = http.StatusServiceUnavailable
					if st.Code() == codes.InvalidArgument {
						statusCode = http.StatusBadRequest
					}
				}

				return "", autoResumeErrored, reverseproxy.NewErrSandboxPlacementRefused(sandboxId, st.Message(), code, statusCode)
			}
			if st.Code() == codes.Unavailable || proxygrpc.RetryablePlacementFailure(code) {
				return "", autoResumeErrored, reverseproxy.NewErrSandboxCapacity(sandboxId, st.Message())
			}
		}

		return "", autoResumeErrored, err
	}

	nodeIP, err = normalizeNodeIP(nodeIP)
	if err != nil {
		return "", autoResumeErrored, err
	}

	return nodeIP, autoResumeSucceeded, nil
}

// NewClientProxy returns the proxy and, when resumes are enabled, the closer
// that stops the resume flights still running after the proxy has shut down.
func NewClientProxy(meterProvider metric.MeterProvider, serviceName string, port, orchestratorProxyPort uint16, catalog catalog.SandboxesCatalog, sandboxResumer SandboxResumer, featureFlagsClient *featureflags.Client) (*reverseproxy.Proxy, ResumeCloser, error) {
	meter := meterProvider.Meter(serviceName)

	var resumer SandboxResumer
	var resumeCloser ResumeCloser
	if sandboxResumer != nil {
		flights, err := newResumeFlights(meter, sandboxResumer, featureFlagsClient)
		if err != nil {
			return nil, nil, err
		}

		resumer = flights
		resumeCloser = flights
	}

	getTargetFromRequest := reverseproxy.GetTargetFromRequest()
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

			l := logger.L().With(logger.ProxyRequestFields(r, sandboxId, port)...)

			trafficAccessToken := r.Header.Get(proxygrpc.MetadataTrafficAccessToken)
			envdAccessToken := r.Header.Get(proxygrpc.MetadataEnvdHTTPAccessToken)
			nodeIP, err := catalogResolution(ctx, sandboxId, port, trafficAccessToken, envdAccessToken, catalog, resumer)
			if err != nil {
				if resumeDeniedErr, ok := errors.AsType[*reverseproxy.SandboxResumePermissionDeniedError](err); ok {
					l.Warn(ctx, "sandbox resume denied", zap.Error(err))

					return nil, resumeDeniedErr
				}

				if resourceExhaustedErr, ok := errors.AsType[*reverseproxy.SandboxResourceExhaustedError](err); ok {
					l.Warn(ctx, "sandbox resource exhausted", zap.Error(err))

					return nil, resourceExhaustedErr
				}

				if stillTransitioningErr, ok := errors.AsType[*reverseproxy.SandboxStillTransitioningError](err); ok {
					l.Warn(ctx, "sandbox still transitioning", zap.Error(err))

					return nil, stillTransitioningErr
				}

				// Logged here with the request's fields; the lot-full shed
				// is counted and stays quiet.
				if capacityErr, ok := errors.AsType[*reverseproxy.SandboxCapacityError](err); ok {
					fields := []zap.Field{zap.Error(err), zap.String("reason", capacityErr.Message), zap.Bool("permanent", capacityErr.Permanent)}
					if capacityErr.Message == reasonParkingLotFull {
						l.Debug(ctx, capacityErr.Error(), fields...)
					} else {
						l.Warn(ctx, capacityErr.Error(), fields...)
					}

					return nil, capacityErr
				}

				// The caller left while it waited: nothing went wrong on
				// this side, and nobody is left to read a page.
				if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
					return nil, err
				}

				if errors.Is(err, ErrNodeRouteUnavailable) {
					l.Warn(ctx, "sandbox route unavailable", zap.Error(err))
				} else if !errors.Is(err, ErrNodeNotFound) {
					l.Warn(ctx, "failed to resolve node ip with Redis resolution", zap.Error(err))
				}

				return nil, reverseproxy.NewErrSandboxNotFound(sandboxId)
			}

			url := &url.URL{
				Scheme: "http",
				Host:   net.JoinHostPort(nodeIP, strconv.Itoa(int(orchestratorProxyPort))),
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
				MaskRequestHost: clientProxyMaskRequestHost(
					ctx,
					featureFlagsClient,
					r.Host,
					sandboxId,
					port,
				),
			}, nil
		},
		nil,
		false,
	)

	_, err := telemetry.GetObservableUpDownCounter(
		meter, telemetry.ClientProxyPoolConnectionsMeterCounterName, func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(proxy.CurrentPoolConnections())

			return nil
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("error registering client proxy connections metric (%s): %w", telemetry.ClientProxyPoolConnectionsMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(
		meter, telemetry.ClientProxyPoolSizeMeterCounterName, func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(int64(proxy.CurrentPoolSize()))

			return nil
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("error registering client proxy pool size metric (%s): %w", telemetry.ClientProxyPoolSizeMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(
		meter, telemetry.ClientProxyServerConnectionsMeterCounterName, func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(proxy.CurrentServerConnections())

			return nil
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("error registering client proxy server connections metric (%s): %w", telemetry.ClientProxyServerConnectionsMeterCounterName, err)
	}

	return proxy, resumeCloser, nil
}
