package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	reverse_proxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	minSandboxConns = 1
	idleTimeout     = 630 * time.Second
)

func NewSandboxProxy(port uint, sandboxes *smap.Map[*sandbox.Sandbox]) (*reverse_proxy.Proxy, error) {
	proxy, err := reverse_proxy.New(
		port,
		minSandboxConns,
		idleTimeout,
		func(r *http.Request) (*client.ProxingInfo, error) {
			sandboxId, port, err := reverse_proxy.ParseHost(r.Host)
			if err != nil {
				return nil, err
			}

			sbx, found := sandboxes.Get(sandboxId)
			if !found {
				return nil, reverse_proxy.NewErrSandboxNotFound(sandboxId)
			}

			url := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s:%d", sbx.Slot.HostIP(), port),
			}

			return &client.ProxingInfo{
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
	if err != nil {
		return nil, err
	}

	_, err = meters.GetObservableUpDownCounter(meters.OrchestratorProxyActiveConnectionsCounterMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentServerConnections()))

		return nil
	})

	if err != nil {
		zap.L().Error("Error registering orchestrator proxy connections metric", zap.Any("metric_name", meters.OrchestratorProxyActiveConnectionsCounterMeterName), zap.Error(err))
	}

	return proxy, nil
}
