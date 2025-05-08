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
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	minSandboxConns = 1
	// This timeout should be > 600 (GCP LB upstream idle timeout) to prevent race condition
	// Also it's a good practice to set it to higher values as you progress in the stack
	// https://cloud.google.com/load-balancing/docs/https#timeouts_and_retries%23:~:text=The%20load%20balancer%27s%20backend%20keepalive,is%20greater%20than%20600%20seconds
	idleTimeout = 630 * time.Second
)

func NewSandboxProxy(port uint, sandboxes *smap.Map[*sandbox.Sandbox]) (*reverse_proxy.Proxy, error) {
	proxy := reverse_proxy.New(
		port,
		minSandboxConns,
		idleTimeout,
		func(r *http.Request) (*pool.Destination, error) {
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

			return &pool.Destination{
				Url:                                url,
				SandboxId:                          sbx.Config.SandboxId,
				SandboxPort:                        port,
				DefaultToPortError:                 true,
				IncludeSandboxIdInProxyErrorLogger: true,
				// We need to include id unique to sandbox to prevent reuse of connection to the same IP:port pair by different sandboxes reusing the network slot.
				// We are not using sandbox id to prevent removing connections based on sandbox id (pause/resume race condition).
				ConnectionKey: sbx.StartID,
				RequestLogger: zap.L().With(
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

	_, err := meters.GetObservableUpDownCounter(meters.OrchestratorProxyServerConnectionsMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentServerConnections()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error registering orchestrator proxy connections metric (%s): %w", meters.OrchestratorProxyServerConnectionsMeterCounterName, err)
	}

	_, err = meters.GetObservableUpDownCounter(meters.OrchestratorProxyPoolConnectionsMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentPoolConnections()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error registering orchestrator proxy connections metric (%s): %w", meters.OrchestratorProxyPoolConnectionsMeterCounterName, err)
	}

	_, err = meters.GetObservableUpDownCounter(meters.OrchestratorProxyPoolSizeMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentPoolSize()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error registering orchestrator proxy pool size metric (%s): %w", meters.OrchestratorProxyPoolSizeMeterCounterName, err)
	}

	return proxy, nil
}
