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

	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	orchestratorspool "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
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

var dnsClient = dns.Client{}

func dnsResolution(sandboxId string, logger *zap.Logger) (string, error) {
	var err error

	msg := new(dns.Msg)
	msg.SetQuestion(fmt.Sprintf("%s.", sandboxId), dns.TypeA)

	var node string
	for i := range maxRetries {
		// Send the query to the server
		resp, _, dnsErr := dnsClient.Exchange(msg, dnsServer)

		// The api server wasn't found, maybe the API server is rolling and the DNS server is not updated yet
		if dnsErr != nil || len(resp.Answer) == 0 {
			err = dnsErr
			logger.Warn(fmt.Sprintf("host for sandbox %s not found: %s", sandboxId, err), zap.Error(err), zap.Int("retry", i+1))

			// Jitter
			time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
			continue
		}

		node = resp.Answer[0].(*dns.A).A.String()

		// The sandbox was not found, we want to return this information to the user
		if node == "127.0.0.1" {
			return "", reverseproxy.NewErrSandboxNotFound(sandboxId)
		}

		break
	}

	// There's no answer, we can't proxy the request
	if err != nil {
		logger.Error("DNS resolving for sandbox failed", l.WithSandboxID(sandboxId), zap.Error(err))

		return "", fmt.Errorf("DNS resolving for sandbox failed: %w", err)
	}

	logger = logger.With(zap.String("node", node))
	return node, nil
}

func NewClientProxy(port uint, catalog *sandboxes.SandboxesCatalog, orchestrators *orchestratorspool.OrchestratorsPool) (*reverseproxy.Proxy, error) {
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
				zap.String("sandbox_id", sandboxId),
				zap.Uint64("sandbox_req_port", port),
				zap.String("sandbox_req_path", r.URL.Path),
			)

			var nodeIp string

			// try to get the sandbox from the catalog (backed by redis)
			s, err := catalog.GetSandbox(sandboxId)
			if err != nil {
				if !errors.Is(err, sandboxes.SandboxNotFoundError) {
					return nil, fmt.Errorf("error getting node where sandbox is placed: %w", err)
				}

				// fallback to api dns resolution
				record, err := dnsResolution(sandboxId, logger)
				if err != nil {
					return nil, err
				}
				nodeIp = record
			} else {
				o, ok := orchestrators.GetOrchestrator(s.OrchestratorId)
				if !ok {
					return nil, fmt.Errorf("orchestrator %s for sandbox %s not found", s.OrchestratorId, sandboxId)
				}

				nodeIp = o.Ip
			}

			// We've resolved the node to proxy the request to
			logger.Debug("Proxying request", zap.String("node_ip", nodeIp))

			url := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s:%d", nodeIp, orchestratorProxyPort),
			}

			return &pool.Destination{
				Url:           url,
				SandboxId:     sandboxId,
				RequestLogger: logger,
				SandboxPort:   port,
				ConnectionKey: clientProxyConnectionKey,
			}, nil
		},
	)

	_, err := meters.GetObservableUpDownCounter(meters.ClientProxyPoolConnectionsMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentServerConnections()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error registering client proxy connections metric (%s): %w", meters.ClientProxyPoolConnectionsMeterCounterName, err)
	}

	_, err = meters.GetObservableUpDownCounter(meters.ClientProxyPoolSizeMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentPoolSize()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error registering client proxy pool size metric (%s): %w", meters.ClientProxyPoolSizeMeterCounterName, err)
	}

	_, err = meters.GetObservableUpDownCounter(meters.ClientProxyServerConnectionsMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentPoolConnections()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error registering client proxy server connections metric (%s): %w", meters.ClientProxyServerConnectionsMeterCounterName, err)
	}

	return proxy, nil
}
