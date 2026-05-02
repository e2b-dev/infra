package proxy

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/connlimit"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// This timeout should be > 600 (GCP LB upstream idle timeout) to prevent race condition
	// Also it's a good practice to set it to higher values as you progress in the stack
	// https://cloud.google.com/load-balancing/docs/https#timeouts_and_retries%23:~:text=The%20load%20balancer%27s%20backend%20keepalive,is%20greater%20than%20600%20seconds
	idleTimeout = 620 * time.Second

	trafficAccessTokenHeader = "e2b-traffic-access-token"
)

var _ sandbox.MapSubscriber = (*SandboxProxy)(nil)

type SandboxProxy struct {
	proxy   *reverseproxy.Proxy
	limiter *connlimit.ConnectionLimiter
}

func NewSandboxProxy(meterProvider metric.MeterProvider, port uint16, sandboxes *sandbox.Map, featureFlags *featureflags.Client) (*SandboxProxy, error) {
	getTargetFromRequest := reverseproxy.GetTargetFromRequest(reverseproxy.HeaderRoutingDisabled)
	limiter := connlimit.NewConnectionLimiter()
	metrics := NewMetrics(meterProvider)

	meter := meterProvider.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/proxy")

	connLimitConfig := &reverseproxy.ConnectionLimitConfig{
		Limiter: limiter,
		GetMaxLimit: func(ctx context.Context) int {
			return featureFlags.IntFlag(ctx, featureflags.SandboxMaxIncomingConnections)
		},
		OnConnectionAcquired: metrics.RecordConnectionsPerSandbox,
		OnConnectionReleased: metrics.RecordConnectionDuration,
		OnConnectionBlocked:  metrics.RecordConnectionBlocked,
	}

	proxy := reverseproxy.New(
		port,
		// Retry 5 times to handle port forwarding delays in sandbox envd.
		reverseproxy.SandboxProxyRetries,
		idleTimeout,
		func(r *http.Request) (*pool.Destination, error) {
			sandboxId, port, err := getTargetFromRequest(r)
			if err != nil {
				return nil, err
			}

			sbx, found := sandboxes.Get(sandboxId)
			if !found {
				return nil, reverseproxy.NewErrSandboxNotFound(sandboxId)
			}

			ingress := sbx.Config.GetNetworkIngress()
			accessToken := ingress.GetTrafficAccessToken()

			isNonEnvdTraffic := int64(port) != consts.DefaultEnvdServerPort

			// Handle traffic access token validation.
			// We are skipping envd port as it has its own access validation mechanism.
			if accessToken != "" && isNonEnvdTraffic {
				accessTokenRaw := r.Header.Get(trafficAccessTokenHeader)
				if accessTokenRaw == "" {
					return nil, reverseproxy.NewErrMissingTrafficAccessToken(sandboxId, trafficAccessTokenHeader)
				} else if subtle.ConstantTimeCompare([]byte(accessTokenRaw), []byte(accessToken)) != 1 {
					return nil, reverseproxy.NewErrInvalidTrafficAccessToken(sandboxId, trafficAccessTokenHeader)
				}
			}

			// Handle request host masking only for non-envd traffic.
			var maskRequestHost *string = nil
			if h := ingress.GetMaskRequestHost(); isNonEnvdTraffic && h != "" {
				h = strings.ReplaceAll(h, pool.MaskRequestHostPortPlaceholder, strconv.FormatUint(port, 10))
				maskRequestHost = &h
			}

			url := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s:%d", sbx.Slot.HostIPString(), port),
			}

			logger := logger.L().With(
				append(
					logger.ProxyRequestFields(r, sbx.Runtime.SandboxID, port),
					logger.WithTeamID(sbx.Runtime.TeamID),
					logger.WithSandboxIP(sbx.Slot.HostIPString()),
				)...,
			)

			return &pool.Destination{
				Url:                                url,
				SandboxId:                          sbx.Runtime.SandboxID,
				SandboxPort:                        port,
				DefaultToPortError:                 true,
				IncludeSandboxIdInProxyErrorLogger: true,
				// We need to include id unique to sandbox to prevent reuse of connection to the same IP:port pair by different sandboxes reusing the network slot.
				// We are not using sandbox id to prevent removing connections based on sandbox id (pause/resume race condition).
				ConnectionKey:   sbx.LifecycleID,
				RequestLogger:   logger,
				MaskRequestHost: maskRequestHost,
			}, nil
		},
		connLimitConfig,
		// We are not using keepalives for orchestrator proxy,
		// because the servers inside of the sandbox can be unstable (restarts),
		// and we are also on the same host, so the overhead is minimal.
		true,
	)

	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorProxyServerConnectionsMeterCounterName, func(_ context.Context, observer metric.Int64Observer) error {
		observer.Observe(proxy.CurrentServerConnections())

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error registering orchestrator proxy connections metric (%s): %w", telemetry.OrchestratorProxyServerConnectionsMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorProxyPoolConnectionsMeterCounterName, func(_ context.Context, observer metric.Int64Observer) error {
		observer.Observe(proxy.CurrentPoolConnections())

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error registering orchestrator proxy connections metric (%s): %w", telemetry.OrchestratorProxyPoolConnectionsMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorProxyPoolSizeMeterCounterName, func(_ context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentPoolSize()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error registering orchestrator proxy pool size metric (%s): %w", telemetry.OrchestratorProxyPoolSizeMeterCounterName, err)
	}

	sandboxProxy := &SandboxProxy{
		proxy:   proxy,
		limiter: limiter,
	}

	// Subscribe to sandbox events for cleanup
	sandboxes.Subscribe(sandboxProxy)

	return sandboxProxy, nil
}

func (p *SandboxProxy) Start(ctx context.Context) error {
	return p.proxy.ListenAndServe(ctx)
}

func (p *SandboxProxy) Close(ctx context.Context) error {
	var err error
	select {
	case <-ctx.Done():
		err = p.proxy.Close()
	default:
		err = p.proxy.Shutdown(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to shutdown proxy server: %w", err)
	}

	return nil
}

func (p *SandboxProxy) RemoveFromPool(connectionKey string) error {
	return p.proxy.RemoveFromPool(connectionKey)
}

func (p *SandboxProxy) GetAddr() string {
	return p.proxy.Addr
}

// OnInsert is called when a sandbox is inserted into the map.
func (p *SandboxProxy) OnInsert(_ context.Context, _ *sandbox.Sandbox) {}

// OnNetworkRelease is called when a sandbox's network slot is released.
// Keyed by LifecycleID so the removal is scoped to this sandbox lifecycle.
func (p *SandboxProxy) OnNetworkRelease(_ context.Context, sbx *sandbox.Sandbox) {
	p.limiter.Remove(sbx.LifecycleID)
}
