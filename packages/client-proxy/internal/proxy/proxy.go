package proxy

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/miekg/dns"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	orchestratorspool "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	dnsServer             = "api.service.consul:5353"
	orchestratorProxyPort = 5007 // orchestrator proxy port
	maxRetries            = 3

	// This timeout should be > 600 (GCP LB upstream idle timeout) to prevent race condition
	// Also it's a good practice to set it to a value higher than the idle timeout of the backend service
	// https://cloud.google.com/load-balancing/docs/https#timeouts_and_retries%23:~:text=The%20load%20balancer%27s%20backend%20keepalive,is%20greater%20than%20600%20seconds
	idleTimeout = 610 * time.Second

	// We use a constant connection key, because we don't have to separate connection pools
	// as we need to do when connecting to sandboxes (from orchestrator proxy) to prevent reuse of pool connections
	// by different sandboxes cause failed connections.
	clientProxyConnectionKey = "client-proxy"
)

var (
	dnsClient = dns.Client{}

	ErrNodeNotFound = errors.New("node not found")
)

func dnsResolution(sandboxId string, logger *zap.Logger) (string, error) {
	var err error

	msg := new(dns.Msg)
	msg.SetQuestion(fmt.Sprintf("%s.", sandboxId), dns.TypeA)

	var node string
	for i := range maxRetries {
		// send the query to the server
		resp, _, dnsErr := dnsClient.Exchange(msg, dnsServer)

		// the api server wasn't found, maybe the API server is rolling and the DNS server is not updated yet
		if dnsErr != nil || len(resp.Answer) == 0 {
			err = dnsErr
			logger.Warn(fmt.Sprintf("host for sandbox %s not found: %s", sandboxId, err), zap.Error(err), zap.Int("retry", i+1))

			// Jitter
			time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
			continue
		}

		node = resp.Answer[0].(*dns.A).A.String()

		// the sandbox was not found, we want to return this information to the user
		if node == "127.0.0.1" {
			return "", ErrNodeNotFound
		}

		break
	}

	// there's no answer, we can't proxy the request
	if err != nil {
		return "", ErrNodeNotFound
	}

	return node, nil
}

func catalogResolution(sandboxId string, logger *zap.Logger, catalog sandboxes.SandboxesCatalog, orchestrators *orchestratorspool.OrchestratorsPool) (string, error) {
	s, err := catalog.GetSandbox(sandboxId)
	if err != nil {
		if !errors.Is(err, sandboxes.ErrSandboxNotFound) {
			return "", ErrNodeNotFound
		}
	}

	o, ok := orchestrators.GetOrchestrator(s.OrchestratorId)
	if !ok {
		return "", errors.New("orchestrator not found")
	}

	return o.Ip, nil
}

func NewClientProxy(meterProvider metric.MeterProvider, serviceName string, port uint, catalog sandboxes.SandboxesCatalog, orchestrators *orchestratorspool.OrchestratorsPool, useCatalogResolution bool) (*reverseproxy.Proxy, error) {
	proxy := reverseproxy.New(
		port,
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

			var nodeIp string

			if useCatalogResolution {
				nodeIp, err = catalogResolution(sandboxId, logger, catalog, orchestrators)
				if err != nil {
					if !errors.Is(err, ErrNodeNotFound) {
						logger.Warn("failed to resolve node ip with Redis resolution", zap.Error(err))
					}

					nodeIp, err = dnsResolution(sandboxId, logger)
					if err != nil {
						if !errors.Is(err, ErrNodeNotFound) {
							logger.Warn("failed to resolve node ip with DNS resolution", zap.Error(err))
						}

						return nil, reverseproxy.NewErrSandboxNotFound(sandboxId)
					}
				}
			} else {
				nodeIp, err = dnsResolution(sandboxId, logger)
				if err != nil {
					if !errors.Is(err, ErrNodeNotFound) {
						logger.Warn("failed to resolve node ip with DNS resolution", zap.Error(err))
					}

					return nil, reverseproxy.NewErrSandboxNotFound(sandboxId)
				}
			}

			logger.Debug("Proxying request", zap.String("node_ip", nodeIp))

			return &pool.Destination{
				SandboxId:     sandboxId,
				RequestLogger: logger,
				SandboxPort:   port,
				ConnectionKey: clientProxyConnectionKey,
				Url: &url.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s:%d", nodeIp, orchestratorProxyPort),
				},
			}, nil
		},
	)

	meter := meterProvider.Meter(serviceName)
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.ClientProxyPoolConnectionsMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(proxy.CurrentServerConnections())
		return nil
	},
	)
	if err != nil {
		return nil, fmt.Errorf("error registering client proxy connections metric (%s): %w", telemetry.ClientProxyPoolConnectionsMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(meter, telemetry.ClientProxyPoolSizeMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentPoolSize()))
		return nil
	},
	)
	if err != nil {
		return nil, fmt.Errorf("error registering client proxy pool size metric (%s): %w", telemetry.ClientProxyPoolSizeMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(meter, telemetry.ClientProxyServerConnectionsMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(proxy.CurrentPoolConnections())
		return nil
	},
	)
	if err != nil {
		return nil, fmt.Errorf("error registering client proxy server connections metric (%s): %w", telemetry.ClientProxyServerConnectionsMeterCounterName, err)
	}

	return proxy, nil
}
