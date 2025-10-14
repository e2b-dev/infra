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

	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
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

	// We use a constant connection key, because we don't have to separate connection pools
	// as we need to do when connecting to sandboxes (from orchestrator proxy) to prevent reuse of pool connections
	// by different sandboxes cause failed connections.
	clientProxyConnectionKey = "client-proxy"
)

var ErrNodeNotFound = errors.New("node not found")

func catalogResolution(ctx context.Context, sandboxId string, c catalog.SandboxesCatalog) (string, error) {
	s, err := c.GetSandbox(ctx, sandboxId)
	if err != nil {
		if errors.Is(err, catalog.ErrSandboxNotFound) {
			return "", ErrNodeNotFound
		}

		return "", fmt.Errorf("failed to get sandbox from catalog: %w", err)
	}

	// todo: when we will use edge for orchestrators discovery we can stop sending IP in the catalog
	//  and just resolve node from pool to get the IP of the node
	return s.OrchestratorIP, nil
}

func NewClientProxy(meterProvider metric.MeterProvider, serviceName string, port uint16, catalog catalog.SandboxesCatalog) (*reverseproxy.Proxy, error) {
	proxy := reverseproxy.New(
		port,
		// Retries that are needed to handle port forwarding delays in sandbox envd are handled by the orchestrator proxy
		reverseproxy.ClientProxyRetries,
		idleTimeout,
		func(r *http.Request) (*pool.Destination, error) {
			sandboxId, port, err := reverseproxy.ParseHost(r.Host)
			if err != nil {
				return nil, err
			}

			logger := zap.L().With(
				zap.String("host", r.Host),
				l.WithSandboxID(sandboxId),
				zap.Uint64("sandbox_req_port", port),
				zap.String("sandbox_req_path", r.URL.Path),
			)

			nodeIP, err := catalogResolution(r.Context(), sandboxId, catalog)
			if err != nil {
				if !errors.Is(err, ErrNodeNotFound) {
					logger.Warn("failed to resolve node ip with Redis resolution", zap.Error(err))
				}

				return nil, reverseproxy.NewErrSandboxNotFound(sandboxId)
			}

			logger.Debug("Proxying request", zap.String("node_ip", nodeIP))

			return &pool.Destination{
				SandboxId:     sandboxId,
				RequestLogger: logger,
				SandboxPort:   port,
				ConnectionKey: clientProxyConnectionKey,
				Url: &url.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s:%d", nodeIP, orchestratorProxyPort),
				},
			}, nil
		},
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
