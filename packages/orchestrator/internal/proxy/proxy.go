package proxy

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	reverse_proxy "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/host"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"go.uber.org/zap"
)

const (
	idleTimeout       = 30 * time.Second
	connectionTimeout = 630 * time.Second
)

func NewOrchestratorProxy(
	port uint,
	sandboxes *smap.Map[*sandbox.Sandbox],
) *http.Server {
	return reverse_proxy.New(
		port,
		idleTimeout,
		connectionTimeout,
		func(r *http.Request) (*host.SandboxHost, error) {
			sandboxId, port, err := host.ParseHost(r.Host)
			if err != nil {
				return nil, err
			}

			sbx, found := sandboxes.Get(sandboxId)
			if !found {
				return nil, &host.ErrSandboxNotFound{}
			}

			url := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s:%d", sbx.Slot.HostIP(), port),
			}

			return &host.SandboxHost{
				Url:       url,
				SandboxId: sbx.Config.SandboxId,
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
