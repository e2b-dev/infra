package proxy

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	reverse_proxy "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

const (
	idleTimeout       = 30 * time.Second
	connectionTimeout = 630 * time.Second
)

func NewSandboxProxy(
	port uint,
	sandboxes *smap.Map[*sandbox.Sandbox],
) *http.Server {
	var activeConnections *metric.Int64UpDownCounter

	connectionCounter, err := meters.GetUpDownCounter(meters.OrchestratorProxyActiveConnectionsCounterMeterName)
	if err != nil {
		zap.L().Error("failed to create active connections counter", zap.Error(err))
	} else {
		activeConnections = &connectionCounter
	}

	return reverse_proxy.New(
		port,
		idleTimeout,
		connectionTimeout,
		activeConnections,
		func(r *http.Request) (*reverse_proxy.RoutingTarget, error) {
			sandboxId, port, err := reverse_proxy.ParseHost(r.Host)
			if err != nil {
				return nil, err
			}

			sbx, found := sandboxes.Get(sandboxId)
			if !found {
				return nil, &reverse_proxy.ErrSandboxNotFound{}
			}

			url := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s:%d", sbx.Slot.HostIP(), port),
			}

			return &reverse_proxy.RoutingTarget{
				Url:           url,
				SandboxId:     sbx.Config.SandboxId,
				ConnectionKey: fmt.Sprintf("%s|%s", sbx.Config.SandboxId, sbx.Slot.HostIP()),
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
}
