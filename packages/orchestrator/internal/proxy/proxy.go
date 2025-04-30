package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	reverse_proxy "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/routing"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

const (
	idleTimeout           = 30 * time.Second
	connectionTimeout     = 630 * time.Second
	maxConnectionDuration = 24 * time.Hour
)

func NewSandboxProxy(
	port uint,
	sandboxes *smap.Map[*sandbox.Sandbox],
) *reverse_proxy.Proxy {
	proxy := reverse_proxy.New(
		port,
		idleTimeout,
		1,
		connectionTimeout,
		maxConnectionDuration,
		func(r *http.Request) (*client.RoutingTarget, error) {
			sandboxId, port, err := routing.ParseHost(r.Host)
			if err != nil {
				return nil, err
			}

			sbx, found := sandboxes.Get(sandboxId)
			if !found {
				return nil, routing.NewErrSandboxNotFound(sandboxId)
			}

			url := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s:%d", sbx.Slot.HostIP(), port),
			}

			return &client.RoutingTarget{
				Url:       url,
				SandboxId: sbx.Config.SandboxId,
				// We need to include id unique to sandbox to prevent reuse of connection to the same IP:port pair by different sandboxes reusing the network slot.
				// We are not using sandbox id to prevent removing connections based on sandbox id (pause/resume race condition).
				ConnectionKey: sbx.StartID,
				Logger: zap.L().With(
					zap.String("host", r.Host),
					zap.String("sandbox_id", sbx.Config.SandboxId),
					zap.String("sandbox_ip", sbx.Slot.HostIP()),
					zap.String("team_id", sbx.Config.TeamId),
					zap.String("sandbox_req_port", url.Port()),
					zap.String("sandbox_req_path", r.URL.Path),
				),
			}, nil
		},
	)

	_, err := meters.GetObservableUpDownCounter(meters.OrchestratorProxyActiveConnectionsCounterMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentDownstreamConnections()))

		return nil
	})

	if err != nil {
		zap.L().Error("Error registering orchestrator proxy connections metric", zap.Any("metric_name", meters.OrchestratorProxyActiveConnectionsCounterMeterName), zap.Error(err))
	}

	return proxy
}
