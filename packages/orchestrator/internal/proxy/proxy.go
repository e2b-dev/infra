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
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// This timeout should be > 600 (GCP LB upstream idle timeout) to prevent race condition
	// Also it's a good practice to set it to higher values as you progress in the stack
	// https://cloud.google.com/load-balancing/docs/https#timeouts_and_retries%23:~:text=The%20load%20balancer%27s%20backend%20keepalive,is%20greater%20than%20600%20seconds
	idleTimeout = 620 * time.Second
)

type SandboxProxy struct {
	proxy *reverseproxy.Proxy
}

func NewSandboxProxy(meterProvider metric.MeterProvider, port uint, sandboxes *smap.Map[*sandbox.Sandbox]) (*SandboxProxy, error) {
	proxy := reverseproxy.New(
		port,
		idleTimeout,
		func(r *http.Request) (*pool.Destination, error) {
			sandboxId, port, err := reverseproxy.ParseHost(r.Host)
			if err != nil {
				return nil, err
			}

			sbx, found := sandboxes.Get(sandboxId)
			if !found {
				return nil, reverseproxy.NewErrSandboxNotFound(sandboxId)
			}

			url := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s:%d", sbx.Slot.HostIPString(), port),
			}

			return &pool.Destination{
				Url:                                url,
				SandboxId:                          sbx.Runtime.SandboxID,
				SandboxPort:                        port,
				DefaultToPortError:                 true,
				IncludeSandboxIdInProxyErrorLogger: true,
				// We need to include id unique to sandbox to prevent reuse of connection to the same IP:port pair by different sandboxes reusing the network slot.
				// We are not using sandbox id to prevent removing connections based on sandbox id (pause/resume race condition).
				ConnectionKey: sbx.Runtime.ExecutionID,
				RequestLogger: zap.L().With(
					zap.String("host", r.Host),
					logger.WithSandboxID(sbx.Runtime.SandboxID),
					zap.String("sandbox_ip", sbx.Slot.HostIPString()),
					logger.WithTeamID(sbx.Runtime.TeamID),
					zap.String("sandbox_req_port", url.Port()),
					zap.String("sandbox_req_path", r.URL.Path),
				),
			}, nil
		},
	)

	meter := meterProvider.Meter("orchestrator.proxy.sandbox")
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorProxyServerConnectionsMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(proxy.CurrentServerConnections())

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("register proxy connections metric (%s): %w", telemetry.OrchestratorProxyServerConnectionsMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorProxyPoolConnectionsMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(proxy.CurrentPoolConnections())

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("register proxy pool connections metric (%s): %w", telemetry.OrchestratorProxyPoolConnectionsMeterCounterName, err)
	}

	_, err = telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorProxyPoolSizeMeterCounterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(proxy.CurrentPoolSize()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("register proxy pool size metric (%s): %w", telemetry.OrchestratorProxyPoolSizeMeterCounterName, err)
	}

	return &SandboxProxy{proxy}, nil
}

func (p *SandboxProxy) Start() error {
	return p.proxy.ListenAndServe()
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
		return fmt.Errorf("shutdown proxy server: %w", err)
	}

	return nil
}

func (p *SandboxProxy) RemoveFromPool(connectionKey string) {
	p.proxy.RemoveFromPool(connectionKey)
}

func (p *SandboxProxy) GetAddr() string {
	return p.proxy.Addr
}
