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
	reverse_proxy "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/routing"
)

const (
	dnsServer                       = "api.service.consul:5353"
	orchestratorProxyPort           = 5007 // orchestrator proxy port
	maxRetries                      = 3
	idleTimeout                     = 620 * time.Second
	connectionTimeout               = 10 * time.Second
	connectionsPerOrchestratorProxy = 16
	maxConnectionDuration           = 0 // The connections can be reused indefinitely as they are from client-proxy to orchestrator-proxy
)

var dnsClient = dns.Client{}

func NewClientProxy(port uint) *reverse_proxy.Proxy {
	proxy := reverse_proxy.New(
		port,
		idleTimeout,
		connectionsPerOrchestratorProxy,
		connectionTimeout,
		maxConnectionDuration,
		func(r *http.Request) (*client.RoutingTarget, error) {
			sandboxId, port, err := routing.ParseHost(r.Host)
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
					return nil, routing.NewErrSandboxNotFound(sandboxId)
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

			return &client.RoutingTarget{
				Url:       url,
				SandboxId: sandboxId,
				Logger:    logger,
			}, nil
		},
	)

	_, err := meters.GetObservableUpDownCounter(meters.ActiveConnectionsCounterMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentDownstreamConnections()))

		return nil
	})

	if err != nil {
		zap.L().Error("Error registering client proxy connections metric", zap.Any("metric_name", meters.ActiveConnectionsCounterMeterName), zap.Error(err))
	}

	return proxy
}
