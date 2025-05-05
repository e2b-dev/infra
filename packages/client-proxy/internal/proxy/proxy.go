package proxy

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/miekg/dns"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	reverse_proxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
)

const (
	dnsServer             = "api.service.consul:5353"
	orchestratorProxyPort = 5007 // orchestrator proxy port
	maxRetries            = 3

	minOrchestratorProxyConns = 16
	idleTimeout               = 620 * time.Second

	// We use constant connection key, because we don't have to separate connection pools
	// as we need to do when connections to sandboxes to prevent reuse of pool connections
	// by different sandboxes cause failed connections.
	clientProxyConnectionKey = "client-proxy"
)

var dnsClient = dns.Client{}

func NewClientProxy(port uint) (*reverse_proxy.Proxy, error) {
	proxy := reverse_proxy.New(
		port,
		minOrchestratorProxyConns,
		idleTimeout,
		func(r *http.Request) (*pool.Destination, error) {
			sandboxId, port, err := reverse_proxy.ParseHost(r.Host)
			if err != nil {
				return nil, err
			}

			logger := zap.L().With(
				zap.String("host", r.Host),
				zap.String("sandbox_id", sandboxId),
				zap.Uint64("sandbox_req_port", port),
				zap.String("sandbox_req_path", r.URL.Path),
			)

			msg := new(dns.Msg)

			// Set the question
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
					return nil, reverse_proxy.NewErrSandboxNotFound(sandboxId)
				}

				break
			}

			// There's no answer, we can't proxy the request
			if err != nil {
				logger.Error("DNS resolving for sandbox failed", zap.String("sandbox_id", sandboxId), zap.Error(err))

				return nil, fmt.Errorf("DNS resolving for sandbox failed: %w", err)
			}

			logger = logger.With(zap.String("node", node))

			// We've resolved the node to proxy the request to
			logger.Debug("Proxying request")

			url := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s:%d", node, orchestratorProxyPort),
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
